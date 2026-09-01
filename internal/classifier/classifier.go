package classifier

import (
	"sort"
	"strings"
	"time"

	"worktracker/internal/model"
)

type Options struct {
	AFKGrace          time.Duration
	AutoEnd           time.Duration
	EvidenceFreshness time.Duration
	LockApps          []string
	LockTitleContains []string
}

func Build(windows []model.WindowEvent, afks []model.AFKEvent, contexts []model.ContextEvent, start, end time.Time, opt Options) []model.Segment {
	return BuildWithPassive(windows, afks, contexts, nil, start, end, opt)
}

func BuildWithPassive(windows []model.WindowEvent, afks []model.AFKEvent, contexts []model.ContextEvent, passiveEvents []model.PassiveEvidenceEvent, start, end time.Time, opt Options) []model.Segment {
	afks = mergeAFK(afks, opt.EvidenceFreshness)
	points := []time.Time{start, end}
	for _, w := range windows {
		points = append(points, w.Start, w.End, w.End.Add(opt.EvidenceFreshness))
	}
	for _, a := range afks {
		points = append(points, a.Start, a.End, a.End.Add(opt.EvidenceFreshness))
		if strings.EqualFold(a.Status, "afk") {
			points = append(points, a.Start.Add(opt.AFKGrace))
		}
	}
	for _, c := range contexts {
		points = append(points, c.Start, c.End, c.End.Add(opt.EvidenceFreshness))
	}
	for _, p := range passiveEvents {
		points = append(points, p.Start, p.End, p.End.Add(opt.EvidenceFreshness))
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Before(points[j]) })
	unique := points[:0]
	for _, p := range points {
		if p.Before(start) || p.After(end) {
			continue
		}
		if len(unique) == 0 || !p.Equal(unique[len(unique)-1]) {
			unique = append(unique, p)
		}
	}
	var out []model.Segment
	for i := 0; i+1 < len(unique); i++ {
		left, right := unique[i], unique[i+1]
		if !right.After(left) {
			continue
		}
		mid := left.Add(right.Sub(left) / 2)
		w := findWindow(windows, mid, opt.EvidenceFreshness)
		a := findAFK(afks, mid, opt.EvidenceFreshness)
		c := findContext(contexts, mid, opt.EvidenceFreshness)
		passive := findPassiveEvidence(passiveEvents, mid, opt.EvidenceFreshness)
		s := classify(left, right, mid, w, a, c, passive, opt)
		out = append(out, s)
	}
	return coalesce(out)
}

func classify(left, right, mid time.Time, w *model.WindowEvent, a *model.AFKEvent, c *model.ContextEvent, passive map[string]model.PassiveEvidence, opt Options) model.Segment {
	s := model.Segment{Start: left, End: right, State: model.Untracked, Location: model.Remote, LocationEvidence: model.Fallback, Health: model.Degraded}
	if c != nil {
		s.Location = c.Location
		s.LocationEvidence = c.LocationEvidence
		s.PassiveEvidence = c.PassiveEvidence
		s.Health = c.Health
	}
	if len(passive) > 0 {
		if s.PassiveEvidence == nil {
			s.PassiveEvidence = map[string]model.PassiveEvidence{}
		} else {
			copyOfEvidence := make(map[string]model.PassiveEvidence, len(s.PassiveEvidence)+len(passive))
			for id, evidence := range s.PassiveEvidence {
				copyOfEvidence[id] = evidence
			}
			s.PassiveEvidence = copyOfEvidence
		}
		for id, evidence := range passive {
			s.PassiveEvidence[id] = evidence
			if !evidence.Available {
				s.Health = model.Degraded
			}
		}
	}
	if w == nil {
		return s
	}
	s.ForegroundApp = w.App
	s.ForegroundTitle = w.Title
	if isLocked(*w, opt) {
		s.State = model.Break
		return s
	}
	if c != nil && c.Location == model.Office && (c.LocationEvidence == model.Confirmed || c.LocationEvidence == model.Stale) {
		s.State = model.Working
		return s
	}
	if a == nil {
		return s
	}
	status := strings.ToLower(a.Status)
	if status == "not-afk" || status == "active" {
		s.State = model.Working
		return s
	}
	if status != "afk" {
		return s
	}
	for _, evidence := range s.PassiveEvidence {
		if evidence.Available && evidence.PassiveWork {
			s.State = model.Working
			return s
		}
	}
	if mid.Sub(a.Start) <= opt.AFKGrace {
		s.State = model.Working
	} else {
		s.State = model.Break
	}
	return s
}

func findPassiveEvidence(events []model.PassiveEvidenceEvent, t time.Time, freshness time.Duration) map[string]model.PassiveEvidence {
	type selected struct {
		start, end time.Time
		evidence   model.PassiveEvidence
	}
	latest := map[string]selected{}
	for _, event := range events {
		if !evidenceEligible(event.Start, event.End, t, freshness) {
			continue
		}
		for id, evidence := range event.Evidence {
			current, ok := latest[id]
			if !ok || laterEvidence(event.Start, event.End, current.start, current.end) {
				latest[id] = selected{start: event.Start, end: event.End, evidence: evidence}
			}
		}
	}
	if len(latest) == 0 {
		return nil
	}
	out := make(map[string]model.PassiveEvidence, len(latest))
	for id, item := range latest {
		out[id] = item.evidence
	}
	return out
}

func Calculate(segments []model.Segment, reportEnd time.Time, autoEnd time.Duration) model.DayReport {
	r := model.DayReport{CurrentState: model.Untracked, Timeline: segments, ReportEnd: reportEnd, End: reportEnd}
	if len(segments) > 0 {
		last := segments[len(segments)-1]
		if !reportEnd.Before(last.Start) && !reportEnd.After(last.End) {
			r.CurrentState = last.State
		}
	}
	first := -1
	last := -1
	for i, s := range segments {
		if s.State == model.Working {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	for _, s := range segments {
		right := minTime(s.End, reportEnd)
		if right.After(s.Start) {
			addTotal(&r.ClassifiedCoverageTotals, s.State, right.Sub(s.Start).Seconds())
		}
	}
	if first < 0 {
		return r
	}
	effectiveEnd := reportEnd
	r.Start = segments[first].Start
	if last >= 0 && autoEnd > 0 && reportEnd.Sub(segments[last].End) >= autoEnd {
		effectiveEnd = segments[last].End
		r.AutoEnded = true
	}
	r.End = effectiveEnd
	for _, s := range segments {
		left := maxTime(s.Start, r.Start)
		right := minTime(s.End, effectiveEnd)
		if !right.After(left) {
			continue
		}
		addTotal(&r.Totals, s.State, right.Sub(left).Seconds())
	}
	return r
}

func addTotal(totals *model.Totals, state model.WorkState, seconds float64) {
	switch state {
	case model.Working:
		totals.WorkingSeconds += seconds
	case model.Break:
		totals.BreakSeconds += seconds
	default:
		totals.UntrackedSeconds += seconds
	}
}

func mergeAFK(in []model.AFKEvent, freshness time.Duration) []model.AFKEvent {
	sort.Slice(in, func(i, j int) bool { return in[i].Start.Before(in[j].Start) })
	var out []model.AFKEvent
	for _, e := range in {
		n := len(out)
		gap := max(freshness, time.Second)
		if n > 0 && strings.EqualFold(out[n-1].Status, e.Status) && !e.Start.After(out[n-1].End.Add(gap)) {
			if e.End.After(out[n-1].End) {
				out[n-1].End = e.End
			}
			continue
		}
		out = append(out, e)
	}
	return out
}
func findWindow(xs []model.WindowEvent, t time.Time, freshness time.Duration) *model.WindowEvent {
	return latestWindow(xs, t, freshness)
}
func findAFK(xs []model.AFKEvent, t time.Time, freshness time.Duration) *model.AFKEvent {
	var best *model.AFKEvent
	for i := range xs {
		if evidenceEligible(xs[i].Start, xs[i].End, t, freshness) && (best == nil || laterEvidence(xs[i].Start, xs[i].End, best.Start, best.End)) {
			best = &xs[i]
		}
	}
	return best
}
func findContext(xs []model.ContextEvent, t time.Time, freshness time.Duration) *model.ContextEvent {
	var best *model.ContextEvent
	for i := range xs {
		if evidenceEligible(xs[i].Start, xs[i].End, t, freshness) && (best == nil || laterEvidence(xs[i].Start, xs[i].End, best.Start, best.End)) {
			best = &xs[i]
		}
	}
	return best
}

func latestWindow(xs []model.WindowEvent, t time.Time, freshness time.Duration) *model.WindowEvent {
	var best *model.WindowEvent
	for i := range xs {
		if evidenceEligible(xs[i].Start, xs[i].End, t, freshness) && (best == nil || laterEvidence(xs[i].Start, xs[i].End, best.Start, best.End)) {
			best = &xs[i]
		}
	}
	return best
}

func evidenceEligible(start, end, observationTime time.Time, freshness time.Duration) bool {
	return !end.Before(start) && !observationTime.Before(start) && model.EvidenceFreshAt(end, observationTime, freshness)
}

func laterEvidence(start, end, bestStart, bestEnd time.Time) bool {
	return start.After(bestStart) || (start.Equal(bestStart) && end.After(bestEnd))
}
func isLocked(w model.WindowEvent, o Options) bool {
	a := strings.ToLower(w.App)
	title := strings.ToLower(w.Title)
	for _, x := range o.LockApps {
		if a == strings.ToLower(x) {
			return true
		}
	}
	for _, x := range o.LockTitleContains {
		if strings.Contains(title, strings.ToLower(x)) {
			return true
		}
	}
	return false
}
func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
func coalesce(in []model.Segment) []model.Segment {
	var out []model.Segment
	for _, s := range in {
		n := len(out)
		if n > 0 && same(out[n-1], s) {
			out[n-1].End = s.End
		} else {
			out = append(out, s)
		}
	}
	return out
}
func same(a, b model.Segment) bool {
	if a.State != b.State || a.Location != b.Location || a.LocationEvidence != b.LocationEvidence || a.ForegroundApp != b.ForegroundApp || a.ForegroundTitle != b.ForegroundTitle || a.Health != b.Health || len(a.PassiveEvidence) != len(b.PassiveEvidence) {
		return false
	}
	for id, x := range a.PassiveEvidence {
		y, ok := b.PassiveEvidence[id]
		if !ok || x.State != y.State || x.Available != y.Available || x.PassiveWork != y.PassiveWork {
			return false
		}
	}
	return true
}
