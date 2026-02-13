package cron

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

type cronSchedule struct {
	minute uint64
	hour   uint64
	dom    uint64
	month  uint64
	dow    uint64

	domStar bool
	dowStar bool
}

type cronBounds struct {
	min, max int
	names    map[string]int
}

var (
	cronMinuteBounds = cronBounds{min: 0, max: 59}
	cronHourBounds   = cronBounds{min: 0, max: 23}
	cronDomBounds    = cronBounds{min: 1, max: 31}
	cronMonthBounds  = cronBounds{
		min: 1, max: 12,
		names: map[string]int{
			"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
			"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
		},
	}
	cronDowBounds = cronBounds{
		min: 0, max: 7,
		names: map[string]int{
			"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
		},
	}
)

var cronExprCache sync.Map // map[string]*cronSchedule

func parseCron5(expr string) (*cronSchedule, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, fmt.Errorf("empty cron expression")
	}
	if cached, ok := cronExprCache.Load(expr); ok {
		if s, ok := cached.(*cronSchedule); ok {
			return s, nil
		}
	}

	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("expected 5 fields, found %d", len(fields))
	}

	minute, _, err := parseCronField(fields[0], cronMinuteBounds, false)
	if err != nil {
		return nil, fmt.Errorf("invalid minute field: %w", err)
	}
	hour, _, err := parseCronField(fields[1], cronHourBounds, false)
	if err != nil {
		return nil, fmt.Errorf("invalid hour field: %w", err)
	}
	dom, domStar, err := parseCronField(fields[2], cronDomBounds, false)
	if err != nil {
		return nil, fmt.Errorf("invalid day-of-month field: %w", err)
	}
	month, _, err := parseCronField(fields[3], cronMonthBounds, false)
	if err != nil {
		return nil, fmt.Errorf("invalid month field: %w", err)
	}
	dow, dowStar, err := parseCronField(fields[4], cronDowBounds, true)
	if err != nil {
		return nil, fmt.Errorf("invalid day-of-week field: %w", err)
	}

	s := &cronSchedule{
		minute:  minute,
		hour:    hour,
		dom:     dom,
		month:   month,
		dow:     dow,
		domStar: domStar,
		dowStar: dowStar,
	}
	cronExprCache.Store(expr, s)
	return s, nil
}

func parseCronField(field string, bounds cronBounds, isDow bool) (uint64, bool, error) {
	var bits uint64
	hasStar := false
	for {
		part, rest, hasMore := strings.Cut(field, ",")
		part = strings.TrimSpace(part)
		if part == "" {
			return 0, false, fmt.Errorf("empty segment")
		}
		b, star, err := parseCronSegment(part, bounds, isDow)
		if err != nil {
			return 0, false, err
		}
		bits |= b
		hasStar = hasStar || star
		if !hasMore {
			break
		}
		field = rest
	}
	if bits == 0 {
		return 0, false, fmt.Errorf("no values")
	}
	return bits, hasStar, nil
}

func parseCronSegment(seg string, bounds cronBounds, isDow bool) (uint64, bool, error) {
	base, stepPart, hasStep := strings.Cut(seg, "/")
	step := 1
	if hasStep {
		if strings.Contains(stepPart, "/") {
			return 0, false, fmt.Errorf("too many '/' in %q", seg)
		}
		n, err := strconv.Atoi(stepPart)
		if err != nil {
			return 0, false, fmt.Errorf("bad step %q", stepPart)
		}
		if n <= 0 {
			return 0, false, fmt.Errorf("step must be > 0")
		}
		step = n
	}

	start, end := 0, 0
	star := false

	switch {
	case base == "*" || base == "?":
		start = bounds.min
		end = bounds.max
		star = step == 1
	case strings.Contains(base, "-"):
		left, right, ok := strings.Cut(base, "-")
		if !ok || strings.Contains(right, "-") {
			return 0, false, fmt.Errorf("bad range %q", base)
		}
		var err error
		start, err = parseCronValue(left, bounds)
		if err != nil {
			return 0, false, err
		}
		end, err = parseCronValue(right, bounds)
		if err != nil {
			return 0, false, err
		}
	default:
		val, err := parseCronValue(base, bounds)
		if err != nil {
			return 0, false, err
		}
		start, end = val, val
		if hasStep {
			end = bounds.max
		}
	}

	if start < bounds.min || start > bounds.max {
		return 0, false, fmt.Errorf("value %d out of range", start)
	}
	if end < bounds.min || end > bounds.max {
		return 0, false, fmt.Errorf("value %d out of range", end)
	}
	if end < start {
		return 0, false, fmt.Errorf("range start > end")
	}

	return buildBits(start, end, step, isDow), star, nil
}

func parseCronValue(s string, bounds cronBounds) (int, error) {
	s = strings.TrimSpace(s)
	if n, err := strconv.Atoi(s); err == nil {
		return n, nil
	}
	if bounds.names != nil {
		if v, ok := bounds.names[strings.ToLower(s)]; ok {
			return v, nil
		}
	}
	return 0, fmt.Errorf("bad value %q", s)
}

func buildBits(start, end, step int, isDow bool) uint64 {
	var bits uint64
	for v := start; v <= end; v += step {
		idx := v
		if isDow && idx == 7 {
			idx = 0
		}
		bits |= 1 << uint(idx)
	}
	return bits
}

func (s *cronSchedule) Next(t time.Time) time.Time {
	origLocation := t.Location()
	loc := origLocation

	// 5-field cron is minute granularity.
	t = t.Add(time.Minute - time.Duration(t.Second())*time.Second - time.Duration(t.Nanosecond()))
	added := false
	yearLimit := t.Year() + 5

WRAP:
	if t.Year() > yearLimit {
		return time.Time{}
	}

	for !hasBit(s.month, int(t.Month())) {
		if !added {
			added = true
			t = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc)
		}
		t = t.AddDate(0, 1, 0)
		if t.Month() == time.January {
			goto WRAP
		}
	}

	for !s.dayMatches(t) {
		if !added {
			added = true
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
		}
		t = t.AddDate(0, 0, 1)
		// Keep behavior around DST transitions near midnight.
		if t.Hour() != 0 {
			if t.Hour() > 12 {
				t = t.Add(time.Duration(24-t.Hour()) * time.Hour)
			} else {
				t = t.Add(time.Duration(-t.Hour()) * time.Hour)
			}
		}
		if t.Day() == 1 {
			goto WRAP
		}
	}

	for !hasBit(s.hour, t.Hour()) {
		if !added {
			added = true
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, loc)
		}
		t = t.Add(1 * time.Hour)
		if t.Hour() == 0 {
			goto WRAP
		}
	}

	for !hasBit(s.minute, t.Minute()) {
		if !added {
			added = true
			t = t.Truncate(time.Minute)
		}
		t = t.Add(1 * time.Minute)
		if t.Minute() == 0 {
			goto WRAP
		}
	}

	return t.In(origLocation)
}

func (s *cronSchedule) dayMatches(t time.Time) bool {
	domMatch := hasBit(s.dom, t.Day())
	dowMatch := hasBit(s.dow, int(t.Weekday()))
	if s.domStar || s.dowStar {
		return domMatch && dowMatch
	}
	return domMatch || dowMatch
}

func hasBit(bits uint64, value int) bool {
	if value < 0 || value > 63 {
		return false
	}
	return bits&(1<<uint(value)) != 0
}
