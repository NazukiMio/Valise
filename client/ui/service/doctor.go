package service

import (
	"context"

	"valise/client/porter"
	"valise/client/ssh"
	"valise/core/rpc"
	"valise/core/valise"
)

// DoctorReport is what `valise doctor <host>` shows.
type DoctorReport struct {
	Host                    string            `json:"host"`
	SSH                     ssh.HostConfig    `json:"ssh"`
	ReusesUserControlMaster bool              `json:"reuses_user_control_master"`
	Platform                porter.Platform   `json:"platform"`
	PorterFound             bool              `json:"porter_found"`
	PorterPath              string            `json:"porter_path,omitempty"`
	PorterVersion           string            `json:"porter_version,omitempty"`
	Protocol                string            `json:"protocol,omitempty"`
	Home                    string            `json:"home,omitempty"`
	Tools                   []rpc.Tool        `json:"tools,omitempty"`
	PendingOps              []rpc.PendingOp   `json:"pending_ops,omitempty"`
	Problem                 *valise.UserError `json:"problem,omitempty"`
}

// Doctor probes the porter and runs hello. A missing or incompatible porter
// is reported in Problem; connection failures are returned as errors.
func (s *Service) Doctor(ctx context.Context, host string) (*DoctorReport, error) {
	t, hc, err := s.transport(host)
	if err != nil {
		return nil, err
	}
	return s.doctorOn(ctx, t, hc, s.Tiered(host))
}

// doctorOn checks the porter through t; daily is the daily key's porter,
// which cannot run the shell commands of the probe chain.
func (s *Service) doctorOn(ctx context.Context, t *ssh.Transport, hc ssh.HostConfig, daily bool) (*DoctorReport, error) {
	r := &DoctorReport{Host: t.Host(), SSH: hc, ReusesUserControlMaster: t.ReusesUserControlMaster()}
	found, err := s.porterOn(ctx, t, daily)
	if err != nil {
		ue, ok := valise.As(err)
		if !ok || (ue.Code != "porter_not_found" && ue.Code != "porter_unusable") {
			return nil, err
		}
		r.Problem = ue
		if !daily {
			if r.Platform, err = porter.RemotePlatform(ctx, t); err != nil {
				return nil, err
			}
		}
		return r, nil
	}
	r.PorterFound, r.PorterPath = true, found.Path
	r.PorterVersion, r.Protocol = found.Info.Version, found.Info.Protocol
	r.Platform = porter.Platform{OS: found.Info.OS, Arch: found.Info.Arch}
	if !rpc.Compatible(found.Info.Protocol, rpc.ProtocolVersion) {
		r.Problem = rpc.ProtocolMismatch(rpc.ProtocolVersion, found.Info.Protocol)
		return r, nil
	}
	c, hello, err := ssh.Dial(ctx, t, found.Path, s.Version)
	if err != nil {
		if ue, ok := valise.As(err); ok && ue.Code == "protocol_mismatch" {
			r.Problem = ue
			return r, nil
		}
		return nil, err
	}
	defer c.Close()
	r.PorterVersion, r.Protocol, r.Home = hello.PorterVersion, hello.Protocol, hello.Home
	r.Platform = porter.Platform{OS: hello.OS, Arch: hello.Arch}
	r.Tools, r.PendingOps = hello.Tools, hello.PendingOps
	return r, nil
}
