package bridge

import (
	"path/filepath"
	"regexp"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// A declaration belongs to the independent reviewer. It is only acted upon
// when a successful, current submission evaluation has actually been observed.
const leanFinalDeclaration = "env PROOFBRIDGE_FINAL_REVIEW=1 "
const leanAdoptDeclaration = ": PROOFBRIDGE_FINAL_REVIEW"

const leanAuditProgram = `(?:timeout (?:--kill-after=[0-9]+[smhd]? )?[0-9]+[smhd]? )?make (?:-j ?1 )?final-audit`
const leanLogPath = `(?:/tmp/|(?:\./)?validation/)?[A-Za-z0-9_.-]+\.log`
const leanDate = `\$\(date -u \+(?:%Y%m%dT%H%M%SZ|%Y-%m-%dT%H:%M:%SZ)\)`
const leanLogArg = `(?:` + leanLogPath + `|"\$(?:LOG|log)")`

var (
	leanDirectAudit       = regexp.MustCompile(`^` + leanAuditProgram + `(?: 2>&1)?$`)
	leanPipeAudit         = regexp.MustCompile(`^(?:` + leanAuditProgram + `|bash -o pipefail -c '` + leanAuditProgram + `') 2>&1 \| tee (?:-a )?(` + leanLogArg + `)$`)
	leanRedirectAudit     = regexp.MustCompile(`^(` + leanAuditProgram + `) > ?(` + leanLogPath + `) 2>&1$`)
	leanLogAssign         = regexp.MustCompile(`^(LOG|log)="(?:/tmp/|validation/)[A-Za-z0-9_.-]+(?:\$\{TS\}|` + leanDate + `)\.log"$`)
	leanTimestamp         = regexp.MustCompile(`^TS=` + leanDate + `$`)
	leanPrintStart        = regexp.MustCompile(`^printf 'LOG=%s\\nSTART=%s\\n' "\$log" "` + leanDate + `" \| tee "\$log"$`)
	leanPrintEnd          = regexp.MustCompile(`^printf 'END=%s\\nEXIT=%s\\n' "` + leanDate + `" "\$status" \| tee -a "\$log"$`)
	leanPrintPipelineExit = regexp.MustCompile(`^echo "(AUDIT_EXIT|EXIT)=\$\{PIPESTATUS\[0\]\}"(?: \| tee -a (` + leanLogArg + `))?$`)
	leanAssignExit        = regexp.MustCompile(`^([a-zA-Z_][a-zA-Z_0-9]*)=\$\?$`)
	leanPrintExit         = regexp.MustCompile(`^echo "([A-Za-z_]+)=\$([a-zA-Z_][a-zA-Z_0-9]*)"$`)
	leanTailLog           = regexp.MustCompile(`^tail (?:-n |-)[0-9]+ (` + leanLogPath + `)$`)
)

type leanAuditCommand struct {
	final, adopt bool
	directory    string
	statusLabel  string
	// A redirected audit prints its status first and then reads its own log.
	statusFirst bool
}

// The native CLIs sometimes render shell argv using several adjacent quote
// forms. Decode a single literal shell argv without running or expanding it.
func leanAuditShell(command string) (string, bool) {
	command = strings.TrimSpace(command)
	if len(command) == 0 || len(command) > 16<<10 {
		return "", false
	}
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil || len(f.Stmts) != 1 {
		return command, false
	}
	stmt := f.Stmts[0]
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) < 3 || len(call.Assigns) != 0 || len(stmt.Redirs) != 0 || stmt.Background || stmt.Negated || stmt.Coprocess {
		return command, false
	}
	shell := call.Args[0].Lit()
	if shell != "bash" && shell != "/bin/bash" && shell != "sh" && shell != "/bin/sh" {
		return command, false
	}
	i := 1
	pipefail := len(call.Args) == 5 && call.Args[i].Lit() == "-o" && call.Args[i+1].Lit() == "pipefail"
	if pipefail {
		i += 2
	}
	if len(call.Args) != i+2 || (call.Args[i].Lit() != "-lc" && call.Args[i].Lit() != "-c") {
		return command, false
	}
	word := call.Args[i+1]
	body, ok := literalShellWord(word)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(body), pipefail
}

func literalShellWord(word *syntax.Word) (string, bool) {
	literal := true
	syntax.Walk(word, func(n syntax.Node) bool {
		switch node := n.(type) {
		case nil, *syntax.Word, *syntax.Lit:
		case *syntax.SglQuoted:
			literal = literal && !node.Dollar
		case *syntax.DblQuoted:
			literal = literal && !node.Dollar
		default:
			literal = false
		}
		return literal
	})
	if !literal {
		return "", false
	}
	body, err := expand.Literal(&expand.Config{}, word)
	if err != nil {
		return "", false
	}
	return body, true
}

// A fixed directory prefix binds even older native CLIs that omit the cwd
// from their command event. Accept one absolute literal and a successful cd.
func leanAuditDirectory(body string) (string, string) {
	if !strings.HasPrefix(body, "cd ") {
		return body, ""
	}
	f, err := syntax.NewParser().Parse(strings.NewReader(body), "")
	if err != nil || len(f.Stmts) != 1 {
		return "", ""
	}
	bin, ok := f.Stmts[0].Cmd.(*syntax.BinaryCmd)
	if !ok || bin.Op != syntax.AndStmt || bin.X.Negated || bin.X.Background || len(bin.X.Redirs) > 0 {
		return "", ""
	}
	call, ok := bin.X.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Assigns) > 0 || len(call.Args) != 2 || call.Args[0].Lit() != "cd" {
		return "", ""
	}
	path, ok := literalShellWord(call.Args[1])
	if !ok || !filepath.IsAbs(path) {
		return "", ""
	}
	return body[bin.Y.Pos().Offset():], path
}

// Only these dataflows establish the exit status of make. A terminal pipefail
// pipeline inherits make's failure, a PIPESTATUS capture reads that exact child,
// and a redirected audit may save $? before reading back the very same log.
func parseLeanAuditCommand(raw string) (leanAuditCommand, bool) {
	if !strings.Contains(raw, "final-audit") && !strings.Contains(raw, "PROOFBRIDGE_FINAL_REVIEW") {
		return leanAuditCommand{}, false
	}
	body, pipefail := leanAuditShell(raw)
	body, directory := leanAuditDirectory(body)
	c := leanAuditCommand{directory: directory}
	if body == leanAdoptDeclaration {
		c.final, c.adopt = true, true
		return c, true
	}
	if strings.HasPrefix(body, leanFinalDeclaration) {
		c.final = true
		body = strings.TrimPrefix(body, leanFinalDeclaration)
		return c, leanDirectAudit.MatchString(body)
	}
	f, err := syntax.NewParser().Parse(strings.NewReader(body), "")
	if err != nil || len(f.Stmts) == 0 || len(f.Stmts) > 9 {
		return c, false
	}
	stmts := make([]string, 0, len(f.Stmts))
	for _, s := range f.Stmts {
		if s.Background || s.Coprocess || s.Negated || s.Cmd == nil {
			return c, false
		}
		// Stmt.End includes the statement terminator, when present.
		line := body[s.Pos().Offset():s.End().Offset()]
		stmts = append(stmts, strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), ";")))
	}
	logVar := ""
	i := 0
	for i < len(stmts) {
		s := stmts[i]
		switch {
		case s == "set -o pipefail":
			pipefail = true
		case leanTimestamp.MatchString(s):
		case leanLogAssign.MatchString(s):
			logVar = leanLogAssign.FindStringSubmatch(s)[1]
		case s == `echo "LOG=$LOG"` && logVar == "LOG":
		case leanPrintStart.MatchString(s) && logVar == "log":
		default:
			goto audit
		}
		i++
	}
audit:
	if i == len(stmts) {
		return c, false
	}
	s, tail := stmts[i], stmts[i+1:]
	if leanDirectAudit.MatchString(s) {
		return c, len(tail) == 0
	}
	if m := leanRedirectAudit.FindStringSubmatch(s); m != nil && len(tail) == 3 {
		save, show, read := leanAssignExit.FindStringSubmatch(tail[0]), leanPrintExit.FindStringSubmatch(tail[1]), leanTailLog.FindStringSubmatch(tail[2])
		if save != nil && show != nil && read != nil && save[1] == show[2] && read[1] == m[2] {
			c.statusLabel = show[1]
			c.statusFirst = true
			return c, true
		}
		return c, false
	}
	pipeline := leanPipeAudit.FindStringSubmatch(s)
	if pipeline == nil {
		return c, false
	}
	log := pipeline[1]
	if strings.HasPrefix(log, `"$`) && log != `"$`+logVar+`"` {
		return c, false
	}
	switch len(tail) {
	case 0:
		return c, pipefail
	case 1:
		show := leanPrintPipelineExit.FindStringSubmatch(tail[0])
		if show != nil && (show[2] == "" || show[2] == log) {
			c.statusLabel = show[1]
			return c, true
		}
	case 3:
		if tail[0] == `status=${PIPESTATUS[0]}` && leanPrintEnd.MatchString(tail[1]) && tail[2] == `exit "$status"` && log == `"$log"` {
			c.statusLabel = "EXIT"
			return c, true
		}
	}
	return c, false
}

const leanSubmissionMarker = "FINAL AUDIT PASSED (submission)."

func (c leanAuditCommand) passed(tool RunnerToolEvent) bool {
	if tool.ExitCode == nil || *tool.ExitCode != 0 || !auditToolSucceeded(tool) {
		return false
	}
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(tool.Output), "\r\n", "\n"), "\n")
	if c.adopt {
		return true
	}
	// Every permitted postlude can emit a status and optional timestamp only.
	// A copied pass line earlier in a failing or baseline log cannot qualify.
	markerIndex := len(lines) - 1
	if c.statusLabel != "" {
		eq := c.statusLabel + "=0"
		if c.statusFirst {
			if lines[0] != eq {
				return false
			}
		} else {
			if lines[len(lines)-1] != eq {
				return false
			}
			markerIndex--
			if markerIndex >= 0 && strings.HasPrefix(lines[markerIndex], "END=") {
				markerIndex--
			}
		}
	}
	return markerIndex >= 0 && strings.TrimSuffix(lines[markerIndex], "\r") == leanSubmissionMarker
}
