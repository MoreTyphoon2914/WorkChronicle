package reporting

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"worktracker/internal/activitywatch"
	"worktracker/internal/browsercontext"
	"worktracker/internal/classifier"
	"worktracker/internal/config"
	"worktracker/internal/model"
	"worktracker/internal/workpolicy"
)

type Service struct {
	Config          config.Config
	AW              *activitywatch.Client
	Hostname        string
	DailyEvaluator  workpolicy.Evaluator
	WeeklyEvaluator workpolicy.Evaluator
}
type Buckets struct{ Window, AFK, Context, Browser string }

func New(cfg config.Config) *Service {
	host, _ := os.Hostname()
	service := &Service{
		Config: cfg, AW: activitywatch.New(cfg.Server, cfg.HTTPTimeout(), time.Duration(cfg.RetryMaxSeconds*float64(time.Second))), Hostname: host,
	}
	if daily, err := cfg.DailyStaticEvaluator(); err == nil {
		service.DailyEvaluator = daily
	}
	if weekly, err := cfg.WeeklyStaticEvaluator(); err == nil {
		service.WeeklyEvaluator = weekly
	}
	return service
}

func (s *Service) ResolveBuckets(ctx context.Context) (Buckets, error) {
	all, err := s.AW.Buckets(ctx)
	if err != nil {
		return Buckets{}, err
	}
	w, err := activitywatch.Discover(all, s.Config.WindowBucket, s.Hostname, "window")
	if err != nil {
		return Buckets{}, err
	}
	a, err := activitywatch.Discover(all, s.Config.AFKBucket, s.Hostname, "afk")
	if err != nil {
		return Buckets{}, err
	}
	cid := "aw-watcher-work-context_" + s.Hostname
	if _, ok := all[cid]; !ok {
		for id := range all {
			if strings.EqualFold(id, cid) {
				cid = id
				ok = true
				break
			}
		}
		if !ok {
			cid = ""
		}
	}
	browserID := "aw-watcher-browser-context_" + s.Hostname
	if _, ok := all[browserID]; !ok {
		for id := range all {
			if strings.EqualFold(id, browserID) {
				browserID = id
				ok = true
				break
			}
		}
		if !ok {
			browserID = ""
		}
	}
	return Buckets{Window: w, AFK: a, Context: cid, Browser: browserID}, nil
}

func (s *Service) Day(ctx context.Context, start, end time.Time) (model.DayReport, error) {
	b, err := s.ResolveBuckets(ctx)
	if err != nil {
		return model.DayReport{}, err
	}
	q, err := s.AW.Query(ctx, b.Window, b.AFK, b.Context, b.Browser, start, end)
	if err != nil {
		return model.DayReport{}, err
	}
	w, a, c := activitywatch.Normalize(q)
	passive := normalizeBrowserEvents(q.Browser)
	segments := classifier.BuildWithPassive(w, a, c, passive, start, end, classifier.Options{AFKGrace: s.Config.AFKGrace(), AutoEnd: s.Config.AutoEnd(), EvidenceFreshness: s.Config.StatusStale(), LockApps: s.Config.LockApps, LockTitleContains: s.Config.LockTitleContains})
	r := classifier.Calculate(segments, end, s.Config.AutoEnd())
	if s.DailyEvaluator == nil {
		return model.DayReport{}, fmt.Errorf("daily work evaluator is not configured")
	}
	r.WorkEvaluation = evaluateWork(s.DailyEvaluator, r.Totals.WorkingSeconds)
	r.Date = start.In(mustLocation(s.Config)).Format("2006-01-02")
	if !r.Start.IsZero() {
		r.Usage = Usage(segments, r.Start, r.End)
	}
	return r, nil
}

func normalizeBrowserEvents(events []activitywatch.Event) []model.PassiveEvidenceEvent {
	out := make([]model.PassiveEvidenceEvent, 0, len(events))
	for _, event := range events {
		observation, err := browsercontext.DecodeStored(event.Data)
		if err != nil {
			continue
		}
		end := event.Timestamp.Add(time.Duration(event.Duration * float64(time.Second)))
		out = append(out, observation.PassiveEvent(event.Timestamp, end))
	}
	return out
}

func (s *Service) Today(ctx context.Context, now time.Time) (model.DayReport, error) {
	start, end, err := s.Config.ReportingPeriod(now)
	if err != nil {
		return model.DayReport{}, err
	}
	return s.Day(ctx, start, end)
}

func (s *Service) Week(ctx context.Context, now time.Time) ([]model.DayReport, error) {
	dayStart, _, err := s.Config.ReportingPeriod(now)
	if err != nil {
		return nil, err
	}
	weekday := (int(dayStart.Weekday()) + 6) % 7
	monday := dayStart.AddDate(0, 0, -weekday)
	var out []model.DayReport
	for start := monday; !start.After(dayStart); start = start.AddDate(0, 0, 1) {
		end := start.AddDate(0, 0, 1)
		if end.After(now) {
			end = now
		}
		r, err := s.Day(ctx, start, end)
		if err != nil {
			return nil, fmt.Errorf("report %s: %w", start.Format("2006-01-02"), err)
		}
		out = append(out, r)
	}
	if s.WeeklyEvaluator == nil {
		return nil, fmt.Errorf("weekly work evaluator is not configured")
	}
	applyWeekToDateEvaluation(out, s.WeeklyEvaluator)
	return out, nil
}

func evaluateWork(evaluator workpolicy.Evaluator, workingSeconds float64) workpolicy.Evaluation {
	return evaluator.Evaluate(workpolicy.WorkSummary{Working: time.Duration(workingSeconds * float64(time.Second))})
}

func applyWeekToDateEvaluation(reports []model.DayReport, evaluator workpolicy.Evaluator) {
	var workingSeconds float64
	for i := range reports {
		workingSeconds += reports[i].Totals.WorkingSeconds
		evaluation := evaluateWork(evaluator, workingSeconds)
		reports[i].WeekToDateEvaluation = &evaluation
	}
}

func Usage(segments []model.Segment, start, end time.Time) []model.Usage {
	type key struct{ app, title string }
	m := map[key]*model.Usage{}
	for _, s := range segments {
		if s.ForegroundApp == "" {
			continue
		}
		left := later(s.Start, start)
		right := earlier(s.End, end)
		if !right.After(left) {
			continue
		}
		k := key{strings.ToLower(strings.TrimSpace(s.ForegroundApp)), NormalizeTitle(s.ForegroundTitle)}
		u := m[k]
		if u == nil {
			u = &model.Usage{Executable: s.ForegroundApp, Title: k.title}
			m[k] = u
		}
		d := right.Sub(left).Seconds()
		switch s.State {
		case model.Working:
			u.Durations.Working += d
		case model.Break:
			u.Durations.Break += d
		default:
			u.Durations.Untracked += d
		}
	}
	out := make([]model.Usage, 0, len(m))
	for _, u := range m {
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Executable == out[j].Executable {
			return out[i].Title < out[j].Title
		}
		return out[i].Executable < out[j].Executable
	})
	return out
}

func NormalizeTitle(s string) string {
	parts := strings.FieldsFunc(strings.TrimSpace(s), unicode.IsSpace)
	s = strings.Join(parts, " ")
	r := []rune(s)
	if len(r) > 200 {
		s = string(r[:200])
	}
	return s
}
func mustLocation(c config.Config) *time.Location {
	l, err := c.Location()
	if err != nil {
		return time.Local
	}
	return l
}
func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
