module valise/client

go 1.26

require (
	filippo.io/age v1.3.1
	golang.org/x/crypto v0.45.0
)

require go.yaml.in/yaml/v3 v3.0.4 // indirect

require (
	filippo.io/hpke v0.4.0 // indirect
	golang.org/x/sys v0.38.0 // indirect
	valise/core v0.0.0
	valise/profiles v0.0.0
	valise/testbed v0.0.0
)

replace (
	valise/core => ../core
	valise/testbed => ../testbed
)

replace valise/profiles => ../profiles
