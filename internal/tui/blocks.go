package tui

// Names of the blocks a key opens and closes in the stream.
const (
	blockHelp     = "help"
	blockMap      = "map"
	blockPipeline = "pipeline"
)

func key(k, what string) string {
	return "  " + styleHi.Render(k) + "  " + styleMid.Render(what)
}

func helpLines() []string {
	return []string{
		styleHi.Render("keys"),
		key("n", "ask for the next step"),
		key("m", "the map"),
		key("p", "the pipeline"),
		key("?", "this help"),
		key("q", "quit"),
		"",
		styleHi.Render("the stream"),
		"  " + styleLo.Render("$") + styleMid.Render(" command   ") + styleLo.Render(GlyphMap) + styleMid.Render(" map   ") +
			styleLo.Render(GlyphPipeline) + styleMid.Render(" pipeline"),
		"  " + styleUser.Render(GlyphReport) + styleMid.Render(" note   ") + styleFind.Render("!") + styleMid.Render(" finding   ") +
			styleMid.Render("› assistant"),
	}
}

// demoMapLines is a made-up map, for trying the panel out.
func demoMapLines() []string {
	return []string{
		styleHi.Render("map"),
		styleMid.Render("host 10.10.11.42") + styleLo.Render("  Linux"),
		styleMid.Render("  22/tcp   ssh    OpenSSH 8.9p1"),
		styleMid.Render("  80/tcp   http   nginx 1.18.0"),
		styleLo.Render("    /admin 301   /login 200   /backup/ 200"),
		styleMid.Render("  3306/tcp mysql  MySQL 8.0.32"),
		styleUser.Render("  creds dbadmin:•••••••"),
	}
}

// demoPipelineLines is a made-up plan: a step done, one under way, one waiting. A step that has not
// started shows no reasoning: nothing is shown ahead of time that has not been worked out.
func demoPipelineLines(spin int) []string {
	return []string{
		styleHi.Render("pipeline"),
		"  " + styleDone.Render("✓") + styleMid.Render(" 1  web content discovery on :80"),
		"  " + styleHi.Render(string(spinFrames[spin%len(spinFrames)])) + styleMid.Render(" 2  fetch /backup/ and read what is in it"),
		"  " + styleLo.Render("○ 3  mysql on :3306"),
	}
}
