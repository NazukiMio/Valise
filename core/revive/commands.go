package revive

func NPMInstallArgv(pkg, version, registry string) []string {
	args := []string{"npm", "install", "-g", "--no-fund", "--no-audit", pkg + "@" + version}
	if registry != "" {
		args = append(args, "--registry", registry)
	}
	return args
}
