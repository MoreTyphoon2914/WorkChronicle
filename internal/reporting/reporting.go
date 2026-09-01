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
	return s.day(ctx, start, end, false)
}

func (s *Service) day(ctx context.Context, start, end time.Time, live bool) (model.DayReport, error) {
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
	if s.DailyEvaluator == nil {
		return model.DayReport{}, fmt.Errorf("daily work evaluator is not configured")
	}
	return BuildDay(start.In(mustLocation(s.Config)), end, segments, live, s.Config.AutoEnd(), s.DailyEvaluator), nil
}

// BuildDay derives report output from already-classified segments. It contains
// no evidence interpretation and is shared by ActivityWatch and Docker Core.
func BuildDay(start, end time.Time, segments []model.Segment, live bool, autoEnd time.Duration, evaluator workpolicy.Evaluator) model.DayReport {
	r := classifier.Calculate(segments, end, autoEnd)
	r.WorkEvaluation = evaluateWork(evaluator, r.Totals.WorkingSeconds)
	r.Date = start.Format("2006-01-02")
	finalizeDay(&r, end, live)
	if r.FirstWorkAt != nil {
		r.Usage = Usage(segments, r.Start, r.End)
	}
	return r
}

func finalizeDay(r *model.DayReport, reportEnd time.Time, live bool) {
	r.ReportEnd = reportEnd
	r.Live = live
	r.WorkBand = r.WorkEvaluation.Band
	r.StandardTargetSeconds = r.WorkEvaluation.StandardTargetSeconds
	r.RemainingTargetSeconds = r.WorkEvaluation.StandardTargetRemainingSeconds
	r.OvertimeSeconds = r.WorkEvaluation.OvertimeSeconds

	var first, last time.Time
	for _, segment := range r.Timeline {
		if segment.State != model.Working {
			continue
		}
		left := segment.Start
		right := earlier(segment.End, r.End)
		if !right.After(left) {
			continue
		}
		if first.IsZero() || left.Before(first) {
			first = left
		}
		if last.IsZero() || right.After(last) {
			last = right
		}
	}
	if !first.IsZero() {
		r.FirstWorkAt = &first
	}
	if !last.IsZero() {
		r.LastWorkAt = &last
	}
	if live {
		if r.RemainingTargetSeconds > 0 {
			finish := reportEnd.Add(time.Duration(r.RemainingTargetSeconds * float64(time.Second)))
			r.EstimatedFinish = &finish
		}
	} else {
		r.FinalState = r.CurrentState
		// CurrentState remains populated as a deprecated compatibility alias for
		// existing day-array JSON consumers. New presentation uses FinalState.
	}
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
	return s.day(ctx, start, end, true)
}

func (s *Service) Week(ctx context.Context, now time.Time) ([]model.DayReport, error) {
	report, err := s.WeekReport(ctx, now)
	if err != nil {
		return nil, err
	}
	return report.Days, nil
}

func (s *Service) WeekReport(ctx context.Context, now time.Time) (model.WeekReport, error) {
	dayStart, _, err := s.Config.ReportingPeriod(now)
	if err != nil {
		return model.WeekReport{}, err
	}
	weekday := (int(dayStart.Weekday()) + 6) % 7
	monday := dayStart.AddDate(0, 0, -weekday)
	var out []model.DayReport
	for start := monday; !start.After(dayStart); start = start.AddDate(0, 0, 1) {
		end := start.AddDate(0, 0, 1)
		if end.After(now) {
			end = now
		}
		r, err := s.day(ctx, start, end, start.Equal(dayStart))
		if err != nil {
			return model.WeekReport{}, fmt.Errorf("report %s: %w", start.Format("2006-01-02"), err)
		}
		out = append(out, r)
	}
	if s.WeeklyEvaluator == nil {
		return model.WeekReport{}, fmt.Errorf("weekly work evaluator is not configured")
	}
	ApplyWeekToDateEvaluation(out, s.WeeklyEvaluator)
	return SummarizeWeek(out, monday, now.In(mustLocation(s.Config)), s.Config.WorkTargets.WorkdaysPerWeek, s.WeeklyEvaluator), nil
}

func SummarizeWeek(reports []model.DayReport, periodStart, periodEnd time.Time, configuredWorkdays int, evaluator workpolicy.Evaluator) model.WeekReport {
	report := model.WeekReport{SchemaVersion: 2, PeriodStart: periodStart, PeriodEnd: periodEnd, Days: reports}
	for _, day := range reports {
		report.Totals.WorkingSeconds += day.Totals.WorkingSeconds
		report.Totals.BreakSeconds += day.Totals.BreakSeconds
		report.Totals.UntrackedSeconds += day.Totals.UntrackedSeconds
		report.ClassifiedCoverageTotals.WorkingSeconds += day.ClassifiedCoverageTotals.WorkingSeconds
		report.ClassifiedCoverageTotals.BreakSeconds += day.ClassifiedCoverageTotals.BreakSeconds
		report.ClassifiedCoverageTotals.UntrackedSeconds += day.ClassifiedCoverageTotals.UntrackedSeconds
	}
	report.AverageDenominator = elapsedWorkdays(periodEnd, configuredWorkdays)
	if report.AverageDenominator > 0 {
		report.AverageWorkingSeconds = report.Totals.WorkingSeconds / float64(report.AverageDenominator)
	}
	report.WorkEvaluation = evaluateWork(evaluator, report.Totals.WorkingSeconds)
	return report
}

func elapsedWorkdays(periodEnd time.Time, configuredWorkdays int) int {
	if configuredWorkdays <= 0 {
		return 0
	}
	elapsedCalendarDays := (int(periodEnd.Weekday())+6)%7 + 1
	if elapsedCalendarDays > configuredWorkdays {
		return configuredWorkdays
	}
	return elapsedCalendarDays
}

func evaluateWork(evaluator workpolicy.Evaluator, workingSeconds float64) workpolicy.Evaluation {
	return evaluator.Evaluate(workpolicy.WorkSummary{Working: time.Duration(workingSeconds * float64(time.Second))})
}

func ApplyWeekToDateEvaluation(reports []model.DayReport, evaluator workpolicy.Evaluator) {
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
