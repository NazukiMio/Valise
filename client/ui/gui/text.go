package gui

// text holds the interface's words in one place (docs/UI.md 4), in the
// terms of PRODUCT.md 8.1.
var text = struct {
	navAgents, navServers, notFound                       string
	homeTitle, homeSub, homeEmpty, homeLoading, refresh   string
	listTitle                                             string
	running, stopped, failed, noService, unitUnknown      string
	entryListening, entryClosed                           string
	migratedTo, superseded                                string
	lastLocal, noLocal, localNote                         string
	tabOverview, tabJourney, back                         string
	whereTitle, harnessLabel, serverLabel, serviceLabel   string
	entryLabel, rootsLabel, recentTitle, journeyAll       string
	journeySub, journeyEmpty, journeyLocalOnly, pointBack string
	pointScope, pointFiles, pointCopies, pointWarn        string
	serversTitle, serversSub, serversEmpty                string
	errUnexpected, techInfo                               string
}{
	navAgents:  "我的 Agent",
	navServers: "服务器",
	notFound:   "没有这个页面",

	homeTitle:   "我的 Agent",
	homeSub:     "—— 熟悉的伙伴，都在这里。",
	homeEmpty:   "还没有接入任何 Agent。",
	homeLoading: "正在读取各台服务器上的 Agent…",
	refresh:     "重新读取",
	listTitle:   "已接入的 Agent",

	running:     "服务运行中",
	stopped:     "服务已停止",
	failed:      "服务启动失败",
	noService:   "没有 Valise 控制的服务",
	unitUnknown: "服务状态未知",

	entryListening: "管理页端口在监听",
	entryClosed:    "管理页端口没有监听",

	migratedTo: "已迁移到 %s",
	superseded: "已被替换",

	lastLocal: "本机副本里最近的备份：%s",
	noLocal:   "本机副本里还没有它的备份",
	localNote: "备份记录读自这台电脑上的仓库副本，不连接服务器。",

	tabOverview: "概览",
	tabJourney:  "旅程",
	back:        "我的 Agent",

	whereTitle:   "正在 %s 上",
	harnessLabel: "运行框架",
	serverLabel:  "服务器",
	serviceLabel: "服务",
	entryLabel:   "管理页",
	rootsLabel:   "目录",
	recentTitle:  "最近保存",
	journeyAll:   "查看完整旅程",

	journeySub:       "—— 选择一个保存下来的时刻。",
	journeyEmpty:     "这台电脑的仓库副本里还没有它的备份。",
	journeyLocalOnly: "这里列出的是这台电脑上仓库副本里的备份；服务器上更新的备份要先复制到本机才会出现。",
	pointBack:        "返回旅程",

	pointScope:  "范围",
	pointFiles:  "%d 个文件，%s",
	pointCopies: "这台电脑上的副本",
	pointWarn:   "需要留意",

	serversTitle: "服务器",
	serversSub:   "—— Agent 住在这里。",
	serversEmpty: "还没有添加服务器。",

	errUnexpected: "发生了未预期的错误",
	techInfo:      "技术信息",
}
