package install

import "valise/core/enroll"

// The instance entry executes only its accepted native_cli, native_session_cli
// or the user's login shell over a fresh management connection. It is separate
// from installation's legacy argv entry.
const NativeCapability = "instance-native/1"
const MethodNativeInfo = "instance_native_info"

type NativeInfoParams struct {
	InstanceID string `json:"instance_id"`
}

type NativeInfo struct {
	InstanceID  string                `json:"instance_id"`
	ProfileID   string                `json:"profile_id"`
	Argv        []string              `json:"argv"`
	SessionArgv []string              `json:"session_argv,omitempty"`
	Hints       []string              `json:"hints,omitempty"`
	Service     []enroll.ServiceState `json:"service,omitempty"`
	// Shell: this porter can also open the instance's interactive shell
	// (`native --instance <id> --shell`): the user's own shell with the
	// instance's environment and command path, no command from a request.
	Shell bool `json:"shell,omitempty"`
	// Session: this porter can open the instance's accepted
	// native_session_cli (`native --instance <id> --session`).
	Session bool `json:"session,omitempty"`
}
