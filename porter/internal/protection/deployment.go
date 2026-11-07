package protection

import (
	"context"
	"encoding/json"
	"path/filepath"
	"time"

	cp "valise/core/protection"
	"valise/core/rpc"
	"valise/core/valise"
	porterenroll "valise/porter/internal/enroll"
)

// DeployedPorter is the formal install path that client deploys write
// (client/porter InstallPath).
func DeployedPorter(home string) string {
	return filepath.Join(home, ".local", "bin", "valise-porter")
}

// CheckDeployment is read-only: it runs the formally installed porter's
// `version --json` and requires the protection capability. A process started
// before a downgrade could otherwise adopt a plan the installed binary no
// longer understands. Missing, unusable or silent binaries fail closed.
func CheckDeployment(ctx context.Context, e *porterenroll.Env, home string) error {
	refuse := valise.New("unsupported_capability", "服务器上正式安装的 porter 不支持版本化保护计划", "重新部署声明 "+cp.Capability+" 的 porter 后再提交")
	if e == nil {
		return refuse
	}
	out, err := e.Run(ctx, 30*time.Second, home, DeployedPorter(home), "version", "--json")
	if err != nil || out.Code != 0 {
		return refuse
	}
	var info rpc.VersionInfo
	if json.Unmarshal(out.Stdout, &info) != nil || info.Name != "valise-porter" {
		return refuse
	}
	for _, c := range info.Capabilities {
		if c == cp.Capability {
			return nil
		}
	}
	return refuse
}
