package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"valise/client/config"
	"valise/core/valise"
)

// station: valise station add|list|remove (docs/ARCHITECTURE.md section 7,
// "Valise 管理的远程备份位置").
func (c *cli) station(ctx context.Context, args []string) error {
	switch {
	case args[0] == "add" && len(args) >= 2:
		return c.stationAdd(ctx, args[1:])
	case args[0] == "list" && len(args) == 1:
		list := c.svc.Stations()
		if len(list) == 0 {
			fmt.Fprintln(c.out, "还没有远程备份位置。添加：valise station add <名称> --address <地址> --user <用户> --path <路径>")
		}
		for _, s := range list {
			fmt.Fprintf(c.out, "%s\t%s@%s:%d%s\n", s.Name, s.User, s.Address, s.PortOr22(), s.Path)
		}
		return nil
	case args[0] == "remove" && len(args) == 2:
		if err := c.svc.StationRemove(args[1]); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "已从这台电脑上移除远程备份位置 %s；远程备份位置上的备份、服务器上的记录都没有删除。\n", args[1])
		return nil
	}
	return errUsage
}

func (c *cli) stationAdd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("station add", flag.ContinueOnError)
	addr := fs.String("address", "", "")
	port := fs.Int("port", 22, "")
	user := fs.String("user", "", "")
	path := fs.String("path", "", "")
	login := fs.String("login", config.LoginPassword, "")
	trust := fs.String("trust-fingerprint", "", "")
	anySource := fs.Bool("any-source", false, "")
	pos, err := parseArgs(fs, args, 1)
	if err != nil {
		return err
	}
	st := config.Station{Name: pos[0], Address: *addr, Port: *port, User: *user, Path: *path, Login: config.LoginPassword, AnySource: *anySource}
	if k, ok := strings.CutPrefix(*login, "key:"); ok {
		st.Login, st.LoginKey = config.LoginKey, k
	} else if *login != config.LoginPassword {
		return errUsage
	}
	fmt.Fprintln(c.out, "建议在远程备份位置上为 Valise 开一个专用用户，并且只给它一个目录：万一某台服务器被攻破，影响只限于这个目录。")
	keys, err := c.svc.StationScan(ctx, st)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "第一次连接 %s，Valise 无法自动验证它的身份。请与远程备份位置（NAS）界面显示的指纹比对：\n", st.Address)
	for _, k := range keys {
		fmt.Fprintf(c.out, "  %s  %s\n", k.Type, k.Fingerprint)
	}
	switch {
	case *trust != "":
		if !hasFingerprint(keys, *trust) {
			return valise.New("fingerprint_mismatch", "远程备份位置给出的指纹里没有 "+*trust, "核对指纹；不一致时不要继续")
		}
	case !c.confirm("指纹一致，信任这个远程备份位置？"):
		return valise.New("cancelled", "已取消，没有添加远程备份位置", "")
	}
	if st.Login == config.LoginPassword {
		fmt.Fprintln(c.out, "接下来 OpenSSH 会询问一次远程备份位置用户的密码（Valise 不保存它），用来装 Valise 的钥匙。")
	}
	res, err := c.svc.StationAdd(ctx, st, keys)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "已添加远程备份位置 %s。同步时 Valise 先让服务器直传；服务器连不上它时，经这台电脑中转。\n", st.Name)
	switch {
	case res.Station.RRsync:
		fmt.Fprintln(c.out, "服务器只能凭 Valise 签发的短期证书写入（rrsync：只写、不删、不覆盖，限定在各自的目录）。")
	case res.Station.SFTPServer != "":
		fmt.Fprintln(c.out, "服务器只能凭 Valise 签发的短期证书写入（受限的 sftp：不能删除）。")
	}
	for _, w := range res.Warnings {
		fmt.Fprintln(c.out, "注意："+w)
	}
	return nil
}
