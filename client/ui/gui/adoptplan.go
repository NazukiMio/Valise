package gui

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/identity"
	"valise/core/protection"
	"valise/core/valise"
	"valise/ui/service"
)

func (a *App) adoptPlanCard(c *ui.Context, host string, d service.DetectedInstance, ad *adoption) {
	instID := orText(d.Enrolled, d.ID)
	subPanel(ui.Column(c)).Gap(8).Children(func() {
		ui.Text(c, T("adopt.plan_title")).Bold()
		if ad.planDone {
			notice(c, "ok", T("adopt.plan_done"))
			ui.Row(c).Justify(ui.End).Children(func() {
				ui.PrimaryButton(c, T("adopt.view_agent")).OnClick(func() {
					a.router.Push("/agents/" + host + "/" + instID)
				})
			})
			return
		}
		muted(ui.Text(c, T("adopt.plan_what"))).FontSize(fsSub).Wrap()
		ui.Checkbox(c, &ad.planSched, T("adopt.plan_daily_ask"))
		if ad.planBusy {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c)
				muted(ui.Text(c, T("protect.applying")))
			})
			return
		}
		if ad.planErr != nil {
			errorBox(c, ad.planErr, a.fold("adopt-plan-err-"+host+d.ID))
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c, T("adopt.view_agent")).OnClick(func() {
				a.router.Push("/agents/" + host + "/" + instID)
			})
			ui.PrimaryButton(c, T("adopt.plan_enable")).OnClick(func() {
				a.applyAdoptDefaultPlan(host, instID, ad)
			})
		})
	})
}

func (a *App) applyAdoptDefaultPlan(host, instID string, ad *adoption) {
	if ad.planBusy {
		return
	}
	ad.planBusy, ad.planErr = true, nil
	desired := defaultProtectionDesired(ad.planSched)
	var fallbackRef *identity.AgentRef
	for _, it := range a.agents.value.Items {
		if it.ConnectionID == host && it.Instance.InstanceID == instID && it.AgentRef != nil {
			r := *it.AgentRef
			fallbackRef = &r
			break
		}
	}
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		ref, err := a.resolveAgentRef(ctx, host, instID, fallbackRef)
		if err != nil {
			a.update(func() { ad.planBusy, ad.planErr = false, err })
			return
		}
		var rev protection.Revision
		if pv, gErr := a.svc.GetProtection(ctx, host, protection.GetParams{AgentRef: ref}); gErr == nil {
			switch {
			case pv.ServerPlan != nil:
				rev = pv.ServerPlan.Revision
			case pv.SupersededPlan != nil:
				rev = pv.SupersededPlan.Revision
			}
		}
		fc, err := a.svc.PrepareProtectionChange(ctx, host, protection.PrepareParams{
			AgentRef:         ref,
			ExpectedRevision: rev,
			Desired:          desired,
		})
		if err == nil {
			a.confirm(fc.PlanID)
			var t service.ProtectionTask
			t, err = a.svc.StartProtectionChange(ctx, fc.PlanID, "desktop-"+fc.PlanID)
			for err == nil && t.State != "succeeded" && t.State != "failed" && t.State != "cancelled" && ctx.Err() == nil {
				time.Sleep(time.Second)
				t, err = a.svc.GetProtectionTask(ctx, t.TaskRef)
			}
			if err == nil && t.Error != nil {
				err = taskErr(t.Error)
			}
		}
		a.update(func() {
			ad.planBusy, ad.planErr = false, err
			if err == nil {
				ad.planDone = true
				delete(a.prots, host+"/"+instID)
				a.refreshAgents()
			}
		})
	})
}

func (a *App) resolveAgentRef(ctx context.Context, host, instID string, fallbackRef *identity.AgentRef) (identity.AgentRef, error) {
	if res, err := a.svc.ListAgents(ctx, service.AgentListRequest{ConnectionIDs: []string{host}}); err == nil {
		for _, it := range res.Items {
			if it.ConnectionID == host && it.Instance.InstanceID == instID && it.AgentRef != nil {
				return *it.AgentRef, nil
			}
		}
	}
	if fallbackRef != nil {
		return *fallbackRef, nil
	}
	return identity.AgentRef{}, valise.New("identity_unbound", T("adopt.no_identity"), T("adopt.again"))
}
