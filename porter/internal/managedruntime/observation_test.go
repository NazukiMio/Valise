package managedruntime

import (
	"os"
	"strconv"
	"testing"
)

func TestUnknownManagerPropertiesNeverProveStopped(t *testing.T) {
	d, _, _, _ := definitionFixture(t)
	for _, raw := range []string{"", "ActiveState=inactive\n", "ActiveState=inactive\nActiveState=active\n", "Environment=secret\n", "MainPID=-1\n"} {
		p, err := parseProperties([]byte(raw))
		if err == nil {
			if _, err = classifyProperties(p, d, "/owned/unit"); err == nil {
				t.Fatal("incomplete state accepted", raw)
			}
		}
	}
	p := map[string]string{"Id": d.name, "LoadState": "loaded", "ActiveState": "inactive", "SubState": "dead", "MainPID": "0", "FragmentPath": "/owned/unit", "DropInPaths": "", "NeedDaemonReload": "no", "ControlGroup": "", "UnitFileState": "disabled", "Job": ""}
	ob, err := classifyProperties(p, d, "/owned/unit")
	if err != nil || ob.Stopped || ob.Running {
		t.Fatal("manager metadata alone was treated as process evidence", err)
	}
	p["Job"] = "123"
	pending, err := classifyProperties(p, d, "/owned/unit")
	if err != nil || !pending.PendingJob || pending.Stopped {
		t.Fatal("queued start treated as a stopped environment", err)
	}
	p["Job"] = ""
	p["DropInPaths"] = "/unapproved/override.conf"
	if _, err := classifyProperties(p, d, "/owned/unit"); err == nil {
		t.Fatal("foreign override accepted")
	}
	p["DropInPaths"] = ""
	p["NeedDaemonReload"] = "yes"
	if _, err := classifyProperties(p, d, "/owned/unit"); err == nil {
		t.Fatal("unloaded unit revision accepted")
	}
}

func TestControlGroupCannotReferToAnotherUsersProcesses(t *testing.T) {
	d, _, _, _ := definitionFixture(t)
	uid := strconv.Itoa(os.Getuid())
	good := "/user.slice/user-" + uid + ".slice/user@" + uid + ".service/app.slice/" + d.name
	if _, err := groupPath(good, d.name); err != nil {
		t.Fatal(err)
	}
	for _, group := range []string{"/", "/system.slice/other.service", good + "/../other.service", good + "/child", "/user.slice/user-999999.slice/user@999999.service/app.slice/" + d.name} {
		if _, err := groupPath(group, d.name); err == nil {
			t.Fatal("foreign group accepted", group)
		}
	}
}
