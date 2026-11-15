package gui

import (
	"github.com/egoist/mygo/ui"
	"valise/ui/service"
)

func (a *App) environmentTestDepth(c *ui.Context, t *restoreTestFlow) {
	st := a.health[t.server+"/"+t.instance]
	if st == nil || !st.canEnvironmentTest {
		depth := "restoretest.depth.files"
		if t.depth == "isolated" {
			depth = "restoretest.depth.isolated"
		}
		fact(c, T("restoretest.depth"), T(depth))
		if st != nil && st.environmentUnavailable {
			muted(ui.Text(c, T("restoretest.environment.unavailable_profile"))).Wrap()
		}
		return
	}
	if t.depth == "" {
		t.depth = "files"
	}
	ui.Text(c, T("restoretest.depth")).Bold()
	ui.RadioGroup(c, func() {
		ui.Radio(c, &t.depth, "files", T("restoretest.depth.files"))
		ui.Radio(c, &t.depth, "isolated", T("restoretest.depth.isolated"))
	}).Label(T("restoretest.depth"))
	if t.depth == "isolated" {
		muted(ui.Text(c, T("restoretest.environment.prepare_hint"))).Wrap()
	}
}

func (a *App) environmentTestReport(c *ui.Context, r *service.RestoreTest) {
	ui.Text(c, T("restoretest.depth.isolated")).Bold()
	if m := r.Materials; m != nil && m.Baseline != nil {
		fact(c, T("restoretest.materials.baseline_time"), m.Baseline.Time.Local().Format("2006-01-02 15:04:05"))
		if m.BaselineLocation != nil {
			fact(c, T("restoretest.materials.baseline_location"), journeyLocation(*m.BaselineLocation))
		}
	}
	if r.Environment == nil {
		muted(ui.Text(c, T("restoretest.environment.no_result"))).Wrap()
		return
	}
	e := r.Environment
	value := func(v bool) string {
		if v {
			return T("restoretest.environment.yes")
		}
		return T("restoretest.environment.no")
	}
	fact(c, T("restoretest.environment.materials"), value(e.CandidateBuilt))
	fact(c, T("restoretest.environment.process"), value(e.Probe.ProcessStarted))
	if e.Probe.ProofKind == "unix-json" {
		fact(c, T("restoretest.environment.socket"), value(e.Probe.SocketListening))
		fact(c, T("restoretest.environment.offline"), value(e.Probe.OfflineResponded))
	} else {
		fact(c, T("restoretest.environment.port"), value(e.Probe.PortListening))
		fact(c, T("restoretest.environment.http"), value(e.Probe.HTTPResponded))
	}
	fact(c, T("restoretest.environment.gateway"), value(e.Probe.GatewayPassed))
	fact(c, T("restoretest.environment.stopped"), value(e.Probe.ProcessesStopped))
	muted(ui.Text(c, T("restoretest.environment.result_hint"))).Wrap()
}
