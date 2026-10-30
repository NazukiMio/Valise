module valise/ui

go 1.26

require (
	golang.org/x/term v0.37.0
	valise/client v0.0.0
	valise/core v0.0.0
	valise/testbed v0.0.0
)

require (
	filippo.io/age v1.3.1 // indirect
	filippo.io/hpke v0.4.0 // indirect
	golang.org/x/crypto v0.45.0 // indirect
	golang.org/x/sys v0.38.0 // indirect
)

replace (
	valise/client => ..
	valise/core => ../../core
	valise/testbed => ../../testbed
)
