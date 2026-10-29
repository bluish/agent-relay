package main

import (
	"math"
	"time"
)

// forecast projects the tool's pooled weekly quota: if you always use the account whose
// weekly limit resets soonest (what 'xorp switch <tool>' picks), when does the pool run dry
// at the current burn rate? The 5-hour window is ignored; the horizon is 10 days.
type forecast struct {
	PoolRemainingPct  float64    `json:"pool_remaining_pct"`
	CapacityPct       float64    `json:"capacity_pct"`
	BurnPctPerHour    float64    `json:"burn_pct_per_hour"`
	BurnSource        string     `json:"burn_source"` // history | week_avg | none
	FirstEmptyAt      *time.Time `json:"first_empty_at,omitempty"`
	FirstEmptyAccount string     `json:"first_empty_account,omitempty"`
	PoolEmptyAt       *time.Time `json:"pool_empty_at,omitempty"`
	RefillAt          *time.Time `json:"refill_at,omitempty"` // first reset after the pool runs dry
	NextResetAt       *time.Time `json:"next_reset_at,omitempty"`
	NextResetGainPct  float64    `json:"next_reset_gain_pct,omitempty"`
}

const (
	hour    = 3600.0
	horizon = 10 * 24 * hour
	step    = 900.0
	eps     = 1e-9
)

var inf = math.Inf(1)

type fcAcct struct {
	name                    string
	rem, reset, win, blockd float64
}

func computeForecast(views []*accountView, samples []sample, burnHours int, now time.Time) forecast {
	var accts []*fcAcct
	pool := 0.0
	for _, v := range views {
		w := v.limit("seven_day")
		if w == nil {
			continue
		}
		win := w.WindowSeconds
		if win <= 0 {
			win = 7 * 86400
		}
		c := &fcAcct{name: v.meta.Name, rem: clamp01(1 - w.Used), reset: inf, win: win}
		if w.ResetsAt != nil {
			c.reset = float64(w.ResetsAt.Unix())
		}
		if c.rem <= eps {
			c.blockd = c.reset
		}
		pool += c.rem
		accts = append(accts, c)
	}
	fc := forecast{PoolRemainingPct: round1(pool * 100), CapacityPct: float64(len(accts) * 100), BurnSource: "none"}
	if len(accts) == 0 {
		return fc
	}
	t0 := float64(now.Unix())
	rate, ok := burnFromHistory(samples, burnHours, t0)
	if ok {
		fc.BurnSource = "history"
	} else if rate, ok = burnFromWeek(views, t0); ok {
		fc.BurnSource = "week_avg"
	}
	fc.BurnPctPerHour = math.Round(rate*10000) / 100

	p := project(accts, rate, t0)
	at := func(sec float64) *time.Time { t := time.Unix(int64(sec), 0); return &t }
	if p.firstName != "" {
		fc.FirstEmptyAt, fc.FirstEmptyAccount = at(p.firstAt), p.firstName
	}
	if !math.IsNaN(p.poolEmpty) {
		fc.PoolEmptyAt = at(p.poolEmpty)
	}
	if !math.IsNaN(p.refill) {
		fc.RefillAt = at(p.refill)
	}
	if !math.IsNaN(p.nextReset) {
		fc.NextResetAt, fc.NextResetGainPct = at(p.nextReset), round1(p.nextGain*100)
	}
	return fc
}

func round1(x float64) float64 { return math.Round(x*10) / 10 }

// burnFromHistory sums weekly usage increases across accounts over the last burnHours.
func burnFromHistory(samples []sample, burnHours int, now float64) (float64, bool) {
	var win []sample
	for _, s := range samples {
		if ts := float64(s.TS); ts >= now-float64(burnHours)*hour && ts <= now+60 {
			win = append(win, s)
		}
	}
	if len(win) < 2 {
		return 0, false
	}
	used := 0.0
	for i := 1; i < len(win); i++ {
		for name, b := range win[i].A {
			a, ok := win[i-1].A[name]
			if !ok {
				continue
			}
			wa, okA := a.W["seven_day"]
			wb, okB := b.W["seven_day"]
			if !okA || !okB {
				continue
			}
			ra, rb := a.R["seven_day"], b.R["seven_day"]
			if ra > 0 && rb > 0 && rb-ra > 3600 {
				used += math.Max(0, wb) // the window rolled over
			} else if d := wb - wa; d > 0 {
				used += d
			}
		}
	}
	spanH := float64(win[len(win)-1].TS-win[0].TS) / hour
	if spanH < 1.0/3 {
		return 0, false
	}
	return used / spanH, true
}

// burnFromWeek is the fallback: each account's weekly usage divided by the window elapsed.
func burnFromWeek(views []*accountView, now float64) (float64, bool) {
	rate, any := 0.0, false
	for _, v := range views {
		w := v.limit("seven_day")
		if w == nil || w.ResetsAt == nil {
			continue
		}
		win := w.WindowSeconds
		if win <= 0 {
			win = 7 * 86400
		}
		elapsedH := (win - (float64(w.ResetsAt.Unix()) - now)) / hour
		if elapsedH < 1 {
			continue
		}
		rate += clamp01(w.Used) / elapsedH
		any = true
	}
	return rate, any
}

type projection struct {
	firstAt             float64
	firstName           string
	poolEmpty           float64 // NaN when not within the horizon
	refill              float64 // NaN when the pool never runs dry or no reset follows
	nextReset, nextGain float64 // NaN when no reset within the horizon
}

func project(accts []*fcAcct, rate, now float64) projection {
	pool := func() float64 {
		s := 0.0
		for _, a := range accts {
			s += a.rem
		}
		return s
	}
	p := projection{poolEmpty: math.NaN(), refill: math.NaN(), nextReset: math.NaN()}
	if pool() <= eps {
		p.poolEmpty = now
	}
	consume := func(t0, t1 float64) {
		need, tt := rate*(t1-t0)/hour, t0
		for need > 1e-12 {
			var pick *fcAcct
			for _, c := range accts {
				if c.rem > eps && c.blockd <= tt && (pick == nil || c.reset < pick.reset) {
					pick = c
				}
			}
			if pick == nil {
				return
			}
			take := math.Min(need, pick.rem)
			pick.rem -= take
			need -= take
			tt += take / rate * hour
			if pick.rem <= eps {
				pick.rem, pick.blockd = 0, pick.reset
				if p.firstName == "" {
					p.firstAt, p.firstName = tt, pick.name
				}
				if math.IsNaN(p.poolEmpty) && pool() <= eps {
					p.poolEmpty = tt
				}
			}
		}
	}
	t, end := now, now+horizon
	for t < end {
		t1 := math.Min(t+step, end)
		r := inf
		for _, c := range accts {
			if c.reset > t && c.reset <= t1 && c.reset < r {
				r = c.reset
			}
		}
		if r != inf {
			if rate > 0 {
				consume(t, r)
			}
			gain := 0.0
			for _, c := range accts {
				if c.reset <= r {
					gain += 1 - c.rem
					c.rem, c.blockd = 1, 0
					c.reset += c.win
				}
			}
			if math.IsNaN(p.nextReset) {
				p.nextReset, p.nextGain = r, gain
			}
			if !math.IsNaN(p.poolEmpty) && math.IsNaN(p.refill) {
				p.refill = r
			}
			t = r
			continue
		}
		if rate > 0 {
			consume(t, t1)
		}
		t = t1
	}
	return p
}

// runsDry reports whether the pool empties before quota comes back, at the current burn.
func (fc forecast) runsDry() bool { return fc.PoolEmptyAt != nil }

// outlook is the one-line answer to "am I fine until the reset?", with when formatting times.
func (fc forecast) outlook(when func(time.Time) string) string {
	switch {
	case fc.CapacityPct == 0:
		return ""
	case fc.runsDry() && fc.RefillAt != nil:
		return "dry " + when(*fc.PoolEmptyAt) + " → " + when(*fc.RefillAt)
	case fc.runsDry():
		return "dry " + when(*fc.PoolEmptyAt)
	case fc.NextResetAt != nil:
		return fmtPct(fc.PoolRemainingPct) + " left · resets " + when(*fc.NextResetAt)
	}
	return fmtPct(fc.PoolRemainingPct) + " left"
}
