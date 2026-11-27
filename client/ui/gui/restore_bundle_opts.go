package gui

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"valise/core/revive"
	"valise/ui/service"
)

// batchReviveOpts holds user opt-in flags and explicit confirmations shared by
// bundle restore (WP6) and one-click harness move (WP6).
type batchReviveOpts struct {
	allowDraft           bool
	allowSnapshotProfile bool
	allowVersionMismatch bool
	skipService          bool
	credential           bool
	sourceChoice         string
}

func (o *batchReviveOpts) resetConfirmations() {
	o.credential = false
	o.sourceChoice = ""
}

// buildConfirm constructs a valid service.ReviveConfirm with non-nil Swap and
// UnverifiedSource callbacks after the user has reviewed and confirmed the plan.
func (o batchReviveOpts) buildConfirm(confirmed bool) service.ReviveConfirm {
	decision := service.SourceDecision(o.sourceChoice)
	return service.ReviveConfirm{
		UnverifiedSource: func(service.UnverifiedSource) service.SourceDecision {
			return decision
		},
		Swap: func(ver *revive.StageVerifyResult) bool {
			if !confirmed {
				return false
			}
			return ver == nil || len(ver.Problems) == 0
		},
	}
}

type batchReviveSummary struct {
	hasBlockers     bool
	hasDraft        bool
	hasSnapProfile  bool
	hasBodyMismatch bool
	hasBodyMissing  bool
	hasServiceMiss  bool
	hasSecrets      bool
	unverified      []*service.UnverifiedSource
	allBackupFirst  bool
}

func planBlockerCount(rp *service.RevivePlan) int {
	if rp == nil {
		return 0
	}
	return len(rp.Blockers) + len(rp.Nested)
}

func groupRevivePlans(gp service.HarnessMoveGroupPlan) []*service.RevivePlan {
	if gp.BundlePlan != nil {
		return gp.BundlePlan.Plans
	}
	if gp.SinglePlan != nil {
		return []*service.RevivePlan{gp.SinglePlan}
	}
	return nil
}

func movePlanRevivePlans(plan *service.HarnessMovePlan) []*service.RevivePlan {
	if plan == nil {
		return nil
	}
	var out []*service.RevivePlan
	for _, gp := range plan.Groups {
		out = append(out, groupRevivePlans(gp)...)
	}
	return out
}

func summarizeRevivePlans(plans []*service.RevivePlan) batchReviveSummary {
	sum := batchReviveSummary{allBackupFirst: true}
	for _, rp := range plans {
		if rp == nil {
			continue
		}
		if len(rp.Blockers) > 0 || len(rp.Nested) > 0 {
			sum.hasBlockers = true
		}
		for _, b := range rp.Blockers {
			switch {
			case b.Code == "profile_draft":
				sum.hasDraft = true
			case b.Code == "profile_from_snapshot":
				sum.hasSnapProfile = true
			case b.Code == "body_version_mismatch":
				sum.hasBodyMismatch = true
			case isBodyEnvBlocker(b.Code):
				sum.hasBodyMissing = true
			case isServiceEnvBlocker(b.Code):
				sum.hasServiceMiss = true
			}
		}
		if rp.RestoreSecret || len(rp.Excluded) > 0 {
			sum.hasSecrets = true
		}
		if rp.UnverifiedSource != nil {
			sum.unverified = append(sum.unverified, rp.UnverifiedSource)
			if !rp.UnverifiedSource.BackupFirst {
				sum.allBackupFirst = false
			}
		}
	}
	if len(sum.unverified) == 0 {
		sum.allBackupFirst = false
	}
	return sum
}

// renderRevivePlanDetails renders honest per-member warnings, blockers and
// nested-instance conflicts for a RevivePlan inside a bundle or move-all plan.
func renderRevivePlanDetails(c *ui.Context, keyPrefix string, rp *service.RevivePlan) {
	if rp == nil {
		return
	}
	for i, w := range rp.Warnings {
		notice(c.Key(keyPrefix+"-w-"+strconv.Itoa(i)), "warn", w)
	}
	for i, b := range rp.Blockers {
		msg := b.Reason
		if msg == "" {
			msg = b.Code
		}
		notice(c.Key(keyPrefix+"-b-"+strconv.Itoa(i)), "warn", msg)
	}
	if len(rp.Nested) > 0 {
		notice(c.Key(keyPrefix+"-nested"), "warn", T("restore.nested", "names", strings.Join(rp.Nested, ", ")))
	}
}

// renderBatchReviveChecks draws opt-in blocker overrides (with a Replan button),
// secret confirmation, and unverified-source decision controls. It returns true
// only when every blocker is cleared and every required confirmation is made.
func renderBatchReviveChecks(c *ui.Context, keyPrefix string, sum batchReviveSummary, opts *batchReviveOpts, onReplan func()) bool {
	hasOptIn := sum.hasDraft || sum.hasSnapProfile || sum.hasBodyMismatch || sum.hasBodyMissing || sum.hasServiceMiss
	if hasOptIn {
		subPanel(ui.Column(c.Key(keyPrefix + "-optins"))).Gap(spInside).Children(func() {
			if sum.hasDraft {
				ui.Checkbox(c.Key(keyPrefix+"-draft"), &opts.allowDraft, T("restore.allow_draft"))
			}
			if sum.hasSnapProfile {
				ui.Checkbox(c.Key(keyPrefix+"-snapprof"), &opts.allowSnapshotProfile, T("restore.allow_snapshot"))
			}
			if sum.hasBodyMismatch {
				ui.Checkbox(c.Key(keyPrefix+"-ver"), &opts.allowVersionMismatch, T("restore.allow_version_mismatch"))
			} else if sum.hasBodyMissing {
				ui.Checkbox(c.Key(keyPrefix+"-body"), &opts.allowVersionMismatch, T("restore.allow_body_data_only"))
			}
			if sum.hasServiceMiss {
				ui.Checkbox(c.Key(keyPrefix+"-skipsvc"), &opts.skipService, T("restore.allow_skip_service"))
			}
			ui.Row(c).Justify(ui.End).Children(func() {
				ui.Button(c, T("restore.replan")).OnClick(onReplan)
			})
		})
	}
	if sum.hasSecrets {
		subPanel(ui.Column(c.Key(keyPrefix + "-secrets"))).Gap(spHair).Children(func() {
			ui.Text(c, T("restore.secrets")).Bold().TextColor(palette.Warn)
			muted(ui.Text(c, T("restore.secret_restore_hint"))).Wrap()
			ui.Checkbox(c.Key(keyPrefix+"-cred"), &opts.credential, T("restore.secret_confirm"))
		})
	}
	if len(sum.unverified) > 0 {
		subPanel(ui.Column(c.Key(keyPrefix + "-unverified"))).Gap(spHair).Children(func() {
			ui.Text(c, T("restore.unverified")).Bold().TextColor(palette.Warn)
			for i, u := range sum.unverified {
				muted(ui.Text(c.Key(keyPrefix+"-uv-"+strconv.Itoa(i)), T("restore.unverified_info",
					"id", u.InstanceID, "profile", u.ProfileID, "version", u.ProfileVersion,
					"server", u.SourceServer, "period", u.Period,
					"time", u.CreatedAt.Local().Format("2006-01-02 15:04"), "tier", tierText(u.Tier)))).Wrap().Selectable()
			}
			ui.RadioGroup(c, func() {
				ui.Radio(c.Key(keyPrefix+"-uv-proceed"), &opts.sourceChoice, "proceed", T("restore.proceed"))
				if sum.allBackupFirst {
					ui.Radio(c.Key(keyPrefix+"-uv-bk"), &opts.sourceChoice, "backup_first", T("restore.backup_first"))
					muted(ui.Text(c, T("restore.backup_first_hint"))).Wrap()
				}
			}).Label(T("restore.unverified"))
		})
	}
	if sum.hasBlockers {
		return false
	}
	if sum.hasSecrets && !opts.credential {
		return false
	}
	if len(sum.unverified) > 0 && opts.sourceChoice == "" {
		return false
	}
	return true
}
