package install

// The instance entry executes only its accepted native_cli over a fresh
// management connection. It is separate from installation's legacy argv entry.
const NativeCapability = "instance-native/1"
const MethodNativeInfo = "instance_native_info"

type NativeInfoParams struct {
	InstanceID string `json:"instance_id"`
}

type NativeInfo struct {
	InstanceID string   `json:"instance_id"`
	ProfileID  string   `json:"profile_id"`
	Argv       []string `json:"argv"`
	Hints      []string `json:"hints,omitempty"`
}
