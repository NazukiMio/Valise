package gui

import (
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"valise/core/protection"
)

// schedEdit is the schedule and period/retention state being edited.
type schedEdit struct {
	cadence    string // "off", "hourly", "daily", "weekly", "monthly"
	everyHours string // "1", "2", "4", "6", "8", "12"
	weekdays   map[string]bool
	dayOfMonth string
	at, tz     string
	catchUp    bool
	period     string
	keep       string
}

var (
	hourlyChoices = []string{"1", "2", "4", "6", "8", "12"}
	weekdayKeys   = []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}
)

func scheduleText(s protection.Schedule) string {
	if s.Enabled == nil || !*s.Enabled {
		return T("protect.schedule_off")
	}
	var out string
	switch s.EffectiveCadence() {
	case "hourly":
		h := s.EveryHours
		if h <= 0 {
			h = 4
		}
		out = T("protect.schedule_hourly", "n", h, "tz", s.Timezone)
	case "weekly":
		out = T("protect.schedule_weekly", "days", formatWeekdays(s.Weekdays), "at", s.DailyAt, "tz", s.Timezone)
	case "monthly":
		out = T("protect.schedule_monthly", "day", s.DayOfMonth, "at", s.DailyAt, "tz", s.Timezone)
	default:
		out = T("protect.schedule_on", "at", s.DailyAt, "tz", s.Timezone)
	}
	if s.CatchUpEnabled() {
		out += T("common.dot_sep") + T("protect.catch_up")
	}
	return out
}

func weekdayLabel(w string) string {
	switch w {
	case "Mon":
		return T("protect.weekday.mon")
	case "Tue":
		return T("protect.weekday.tue")
	case "Wed":
		return T("protect.weekday.wed")
	case "Thu":
		return T("protect.weekday.thu")
	case "Fri":
		return T("protect.weekday.fri")
	case "Sat":
		return T("protect.weekday.sat")
	case "Sun":
		return T("protect.weekday.sun")
	}
	return w
}

func cadenceLabel(cad string) string {
	switch cad {
	case "off":
		return T("protect.sched.cadence.off")
	case "hourly":
		return T("protect.sched.cadence.hourly")
	case "daily":
		return T("protect.sched.cadence.daily")
	case "weekly":
		return T("protect.sched.cadence.weekly")
	case "monthly":
		return T("protect.sched.cadence.monthly")
	}
	return cad
}

func periodUnitLabel(u string) string {
	switch u {
	case "week":
		return T("protect.period_unit.week")
	case "quarter":
		return T("protect.period_unit.quarter")
	default:
		return T("protect.period_unit.month")
	}
}

func formatWeekdays(days []string) string {
	set := map[string]bool{}
	for _, d := range days {
		set[d] = true
	}
	var names []string
	for _, w := range weekdayKeys {
		if set[w] {
			names = append(names, weekdayLabel(w))
		}
	}
	return strings.Join(names, T("common.list_sep"))
}

func newSchedEdit(d protection.Desired) *schedEdit {
	cadence := "off"
	if d.Schedule.Enabled != nil && *d.Schedule.Enabled {
		cadence = d.Schedule.EffectiveCadence()
	}
	every := "4"
	if d.Schedule.EveryHours > 0 {
		every = strconv.Itoa(d.Schedule.EveryHours)
	}
	days := map[string]bool{}
	for _, w := range d.Schedule.Weekdays {
		days[w] = true
	}
	dom := "1"
	if d.Schedule.DayOfMonth >= 1 && d.Schedule.DayOfMonth <= 28 {
		dom = strconv.Itoa(d.Schedule.DayOfMonth)
	}
	return &schedEdit{
		cadence:    cadence,
		everyHours: every,
		weekdays:   days,
		dayOfMonth: dom,
		at:         orText(d.Schedule.DailyAt, "04:40"),
		tz:         orText(d.Schedule.Timezone, localZone()),
		catchUp:    d.Schedule.CatchUpEnabled(),
		period:     orText(d.Period.Unit, "month"),
		keep:       strconv.Itoa(d.Period.KeepAutoPeriods),
	}
}

func (a *App) scheduleDialog(c *ui.Context, row *agentRow, ps *protState) {
	e := ps.sched
	d := ps.desired()
	open := true
	if ui.Modal(c, &open, func() {
		dialog(ui.Column(c), 520).Children(func() {
			dialogTitle(ui.Text(c, T("protect.schedule_title")))
			ui.Text(c, T("protect.sched.section_freq")).Bold()
			ui.RadioGroup(c, func() {
				for _, cad := range []string{"off", "hourly", "daily", "weekly", "monthly"} {
					ui.Radio(c.Key("cad-"+cad), &e.cadence, cad, cadenceLabel(cad))
				}
			}).Row().Gap(10).Wrap().Label(T("protect.sched.section_freq"))

			if e.cadence != "off" {
				a.scheduleCadenceFields(c, e)
			}

			ui.Box(c).Height(1).Background(palette.Line)
			ui.Text(c, T("protect.sched.section_period")).Bold()
			labeled(c, T("protect.period"), func() {
				ui.RadioGroup(c, func() {
					for _, u := range []string{"week", "month", "quarter"} {
						ui.Radio(c.Key("p-"+u), &e.period, u, T("protect.period."+u))
					}
				}).Row().Gap(16).Label(T("protect.period"))
			})
			muted(ui.Text(c, T("protect.period_note"))).Wrap()

			keepN, keepOK := 0, true
			if ps.savepointsSupported {
				unitName := periodUnitLabel(e.period)
				labeled(c, T("protect.retention.label", "unit", unitName), func() {
					ui.TextInput(c, &e.keep).Label(T("protect.retention.label", "unit", unitName)).Placeholder("0")
				})
				keepN, keepOK = parseRetention(e.keep)
				if !keepOK {
					ui.Text(c, T("protect.retention.invalid")).FontSize(12).TextColor(palette.Warn).Wrap()
				} else if keepN == 0 {
					muted(ui.Text(c, T("protect.retention.preview_zero"))).Wrap()
				} else {
					muted(ui.Text(c, T("protect.retention.preview_n", "n", keepN, "unit", unitName))).Wrap()
				}
				muted(ui.Text(c, T("protect.retention.why"))).Wrap()
			}

			_, schedOK := buildScheduleFromEdit(e, d.Schedule)
			if !schedOK {
				ui.Text(c, T("protect.sched.invalid")).FontSize(12).TextColor(palette.Warn).Wrap()
			}
			valid := schedOK && keepOK
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c, T("common.cancel")).OnClick(func() { ps.sched = nil })
				ui.PrimaryButton(c, T("protect.save")).Disabled(!valid).OnClick(func() { a.saveSchedule(row, ps) })
			})
		})
	}).Dismissed() {
		ps.sched = nil
	}
}

func (a *App) scheduleCadenceFields(c *ui.Context, e *schedEdit) {
	switch e.cadence {
	case "hourly":
		labeled(c, T("protect.sched.every_hours"), func() {
			ui.RadioGroup(c, func() {
				for _, h := range hourlyChoices {
					ui.Radio(c.Key("hr-"+h), &e.everyHours, h, T("protect.sched.hours_n", "n", h))
				}
			}).Row().Gap(10).Wrap().Label(T("protect.sched.every_hours"))
		})
		ui.Row(c).Gap(10).Children(func() {
			labeled(c, T("protect.sched.hourly_at"), func() { ui.TextInput(c, &e.at).Label(T("protect.sched.hourly_at")).Placeholder("04:40") }).Width(130)
			labeled(c, T("protect.tz"), func() { ui.TextInput(c, &e.tz).Label(T("protect.tz")).Placeholder("Asia/Shanghai") }).Grow(1)
		})
	case "daily":
		ui.Row(c).Gap(10).Children(func() {
			labeled(c, T("protect.at"), func() { ui.TextInput(c, &e.at).Label(T("protect.at")).Placeholder("04:40") }).Width(110)
			labeled(c, T("protect.tz"), func() { ui.TextInput(c, &e.tz).Label(T("protect.tz")).Placeholder("Asia/Shanghai") }).Grow(1)
		})
	case "weekly":
		labeled(c, T("protect.sched.weekdays"), func() {
			ui.Row(c).Gap(8).Wrap().Children(func() {
				if e.weekdays == nil {
					e.weekdays = map[string]bool{}
				}
				for _, w := range weekdayKeys {
					checked := e.weekdays[w]
					if ui.Checkbox(c.Key("wd-"+w), &checked, weekdayLabel(w)).Changed() {
						e.weekdays[w] = checked
					}
				}
			})
		})
		ui.Row(c).Gap(10).Children(func() {
			labeled(c, T("protect.at"), func() { ui.TextInput(c, &e.at).Label(T("protect.at")).Placeholder("04:40") }).Width(110)
			labeled(c, T("protect.tz"), func() { ui.TextInput(c, &e.tz).Label(T("protect.tz")).Placeholder("Asia/Shanghai") }).Grow(1)
		})
	case "monthly":
		ui.Row(c).Gap(10).Children(func() {
			labeled(c, T("protect.sched.day_of_month"), func() { ui.TextInput(c, &e.dayOfMonth).Label(T("protect.sched.day_of_month")).Placeholder("1") }).Width(110)
			labeled(c, T("protect.at"), func() { ui.TextInput(c, &e.at).Label(T("protect.at")).Placeholder("04:40") }).Width(110)
			labeled(c, T("protect.tz"), func() { ui.TextInput(c, &e.tz).Label(T("protect.tz")).Placeholder("Asia/Shanghai") }).Grow(1)
		})
	}
	ui.Checkbox(c, &e.catchUp, T("protect.catch_up_ask"))
}

// buildScheduleFromEdit converts the dialog fields into a validated Schedule.
func buildScheduleFromEdit(e *schedEdit, prev protection.Schedule) (protection.Schedule, bool) {
	if e.cadence == "off" {
		off := false
		dailyAt := strings.TrimSpace(prev.DailyAt)
		if !validHHMM(dailyAt) {
			dailyAt = "04:40"
		}
		tz := strings.TrimSpace(prev.Timezone)
		if !protection.ValidTimezone(tz) {
			tz = localZone()
		}
		return protection.Schedule{Enabled: &off, DailyAt: dailyAt, Timezone: tz}, true
	}
	tz := strings.TrimSpace(e.tz)
	if !protection.ValidTimezone(tz) {
		return protection.Schedule{}, false
	}
	on := true
	catch := e.catchUp
	s := protection.Schedule{Enabled: &on, Timezone: tz, CatchUp: &catch}
	at := strings.TrimSpace(e.at)
	switch e.cadence {
	case "hourly":
		h, err := strconv.Atoi(strings.TrimSpace(e.everyHours))
		if err != nil || !slices.Contains([]int{1, 2, 4, 6, 8, 12}, h) {
			return protection.Schedule{}, false
		}
		if at != "" && !validHHMM(at) {
			return protection.Schedule{}, false
		}
		s.Cadence, s.EveryHours, s.DailyAt = "hourly", h, at
	case "daily":
		if !validHHMM(at) {
			return protection.Schedule{}, false
		}
		s.DailyAt = at
	case "weekly":
		if !validHHMM(at) {
			return protection.Schedule{}, false
		}
		var days []string
		for _, w := range weekdayKeys {
			if e.weekdays[w] {
				days = append(days, w)
			}
		}
		if len(days) == 0 {
			return protection.Schedule{}, false
		}
		s.Cadence, s.DailyAt, s.Weekdays = "weekly", at, days
	case "monthly":
		if !validHHMM(at) {
			return protection.Schedule{}, false
		}
		dom, err := strconv.Atoi(strings.TrimSpace(e.dayOfMonth))
		if err != nil || dom < 1 || dom > 28 {
			return protection.Schedule{}, false
		}
		s.Cadence, s.DailyAt, s.DayOfMonth = "monthly", at, dom
	default:
		return protection.Schedule{}, false
	}
	return s, true
}

func validHHMM(s string) bool {
	if len(s) != 5 || s[2] != ':' {
		return false
	}
	hh, err1 := strconv.Atoi(s[:2])
	mm, err2 := strconv.Atoi(s[3:])
	return err1 == nil && err2 == nil && hh >= 0 && hh <= 23 && mm >= 0 && mm <= 59
}

// saveSchedule applies the schedule dialog's choices.
func (a *App) saveSchedule(row *agentRow, ps *protState) {
	e := ps.sched
	d := ps.desired()
	sched, ok := buildScheduleFromEdit(e, d.Schedule)
	if !ok {
		return
	}
	d.Schedule = sched
	d.Period.Unit = orText(e.period, "month")
	if ps.savepointsSupported {
		keep, ok := parseRetention(e.keep)
		if !ok {
			return
		}
		d.Period.KeepAutoPeriods = keep
	}
	if !protection.ValidTimezone(d.Period.Timezone) {
		d.Period.Timezone = orText(d.Schedule.Timezone, localZone())
	}
	ps.sched = nil
	a.applyDesired(row, ps, d)
}
