package notify

import (
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/tencent/codex-bridge/internal/store"
)

type completionView struct {
	Title, Status, Description, Accent, Background         string
	Total, Input, Output, Cache, Cost, CostNote, UsageNote string
	Link, SubmissionID                                     string
}

func shortMailText(value string, limit int) string {
	value = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value))
	chars := []rune(value)
	if len(chars) > limit {
		return string(chars[:limit]) + "…"
	}
	return value
}

func notificationDetailURL(base, runID string) string {
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	return strings.TrimRight(u.String(), "/") + "/orchestrate/runs/" + url.PathEscape(runID)
}

func mailNumber(n int64) string {
	value := strconv.FormatInt(max(int64(0), n), 10)
	for i := len(value) - 3; i > 0; i -= 3 {
		value = value[:i] + "," + value[i:]
	}
	return value
}

func completionSummary(result store.AutomationResult, n store.AutomationNotification, base string) completionView {
	v := completionView{Title: shortMailText(result.Title, 100), Status: "验证未通过", Description: "运行已结束，但验证尚未通过。请查看详情中的检查结果与未完成项。", Accent: "#b45309", Background: "#fff7ed", Total: "未记录", Input: "未记录", Output: "未记录", Cache: "未记录", Cost: "未记录", CostNote: "暂无可信费用记录", UsageNote: "用量未记录", Link: notificationDetailURL(base, n.RunID), SubmissionID: shortMailText(n.SubmissionID, 100)}
	if v.Title == "" {
		v.Title = "ProofBridge 任务"
	}
	switch result.Execution.Status {
	case store.OrchestrationFailed:
		v.Status, v.Description, v.Accent, v.Background = "执行失败", "任务执行遇到错误，尚不能据此判断命题真伪。请打开详情查看原因。", "#b91c1c", "#fef2f2"
	case store.OrchestrationCanceled:
		v.Status, v.Description = "已取消", "任务已停止，本次未确认验证通过。已有结果保留在详情中。"
	case store.OrchestrationCompleted:
		v.Status = "未完成验证"
		if result.Verification.Status == "pass" {
			v.Status, v.Description, v.Accent, v.Background = "验证通过", "本次任务已完成，验证结果为通过。完整证明与检查记录可在详情中查看。", "#047857", "#ecfdf5"
		}
	default:
		v.Status, v.Description = "结果待确认", "暂时没有可确认的验证结果。请登录查看任务的最新状态。"
	}
	u := result.UsageSummary
	if u != nil && u.Available {
		v.Total, v.Input, v.Output, v.Cache = mailNumber(u.TotalTokens), mailNumber(u.InputTokens+u.CacheTokens), mailNumber(u.OutputTokens), mailNumber(u.CacheTokens)
		v.Cost, v.CostNote, v.UsageNote = "未知", "价格未知，不能视为零费用", "已记录用量"
		if u.AccountingStatus != "complete" {
			v.UsageNote = "用量记录可能不完整"
		}
		if !math.IsNaN(u.EstimatedCostUSD) && !math.IsInf(u.EstimatedCostUSD, 0) && u.EstimatedCostUSD >= 0 && (u.CostKnown || u.EstimatedCostUSD > 0) {
			v.Cost, v.CostNote = fmt.Sprintf("$%.6f", u.EstimatedCostUSD), "估算，非账单"
			if !u.CostKnown {
				v.CostNote = "仅已知部分的估算，未覆盖全部价格"
			}
		}
	}
	return v
}

func FromNotification(n store.AutomationNotification, publicURL string) Message {
	var result store.AutomationResult
	_ = json.Unmarshal(n.Snapshot, &result)
	v := completionSummary(result, n, publicURL)
	if n.Kind == "attention" {
		return Message{ID: n.ID, To: n.Recipient, Subject: "[ProofBridge] 任务需要处理：" + v.Title, Text: fmt.Sprintf("任务需要你的审批或回答。\n任务：%s\n任务编号：%s\n提交编号：%s\n\n请连接实验室 VPN 并登录本人账号查看和处理：\n%s\n\n这不是任务完成报告。邮件不包含审批命令或原生问题正文。", v.Title, n.RunID, n.SubmissionID, v.Link)}
	}
	text := fmt.Sprintf("%s\n%s\n%s\n\n总 Token：%s\n输入（含缓存）：%s，其中缓存：%s\n输出：%s\n费用：%s\n%s；%s\n\n查看详情：\n%s\n\n统计范围：截至本次提交结束的运行累计快照。缓存已包含在输入中，不重复计数。\n费用为估算，非账单。详情需要登录，实验室内网部署可能需要 VPN。\n提交编号：%s\n", v.Status, v.Title, v.Description, v.Total, v.Input, v.Cache, v.Output, v.Cost, v.CostNote, v.UsageNote, v.Link, v.SubmissionID)
	var html strings.Builder
	_ = completionTemplate.Execute(&html, v)
	return Message{ID: n.ID, To: n.Recipient, Subject: "[ProofBridge] " + v.Status + "：" + v.Title, Text: text, HTML: html.String()}
}

var completionTemplate = template.Must(template.New("completion").Parse(`<!doctype html>
<html lang="zh-CN" dir="ltr"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>ProofBridge 任务结果</title></head>
<body style="margin:0;padding:0;background:#f6f7f9;color:#18181b;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Arial,sans-serif;">
<table lang="zh-CN" dir="ltr" role="presentation" width="100%" cellspacing="0" cellpadding="0" style="background:#f6f7f9;"><tr><td align="center" style="padding:32px 16px;">
<table role="presentation" width="100%" cellspacing="0" cellpadding="0" style="max-width:600px;"><tr><td style="padding:0 0 18px;font-size:14px;font-weight:700;letter-spacing:.3px;">ProofBridge</td></tr>
<tr><td style="background:#ffffff;border:1px solid #e4e4e7;border-radius:16px;padding:28px 24px;">
<p style="margin:0 0 18px;"><span style="display:inline-block;padding:6px 12px;border-radius:6px;font-size:14px;line-height:20px;font-weight:700;color:{{.Accent}};background:{{.Background}};">{{.Status}}</span></p>
<h1 style="margin:0 0 12px;font-size:24px;line-height:34px;font-weight:700;overflow-wrap:anywhere;word-break:break-word;">{{.Title}}</h1>
<p style="margin:0 0 24px;font-size:15px;line-height:25px;color:#52525b;">{{.Description}}</p>
<table role="presentation" width="100%" cellspacing="0" cellpadding="0" style="border:1px solid #e4e4e7;border-radius:10px;table-layout:fixed;">
<tr><td width="50%" valign="top" style="padding:18px 14px;border-right:1px solid #e4e4e7;border-bottom:1px solid #e4e4e7;"><p style="margin:0 0 8px;color:#71717a;font-size:12px;">总 Token</p><p style="margin:0;font-size:24px;line-height:32px;font-weight:700;overflow-wrap:anywhere;">{{.Total}}</p><p style="margin:6px 0 0;font-size:11px;line-height:18px;color:#71717a;">{{.UsageNote}}</p></td>
<td width="50%" valign="top" style="padding:18px 14px;border-bottom:1px solid #e4e4e7;"><p style="margin:0 0 8px;color:#71717a;font-size:12px;">费用估算 · USD</p><p style="margin:0;font-size:22px;line-height:32px;font-weight:700;overflow-wrap:anywhere;">{{.Cost}}</p><p style="margin:6px 0 0;font-size:11px;line-height:18px;color:#71717a;">{{.CostNote}}</p></td></tr>
<tr><td width="50%" valign="top" style="padding:16px 14px;border-right:1px solid #e4e4e7;"><p style="margin:0 0 8px;color:#71717a;font-size:12px;">输入（含缓存）</p><p style="margin:0;font-size:18px;line-height:26px;font-weight:600;overflow-wrap:anywhere;">{{.Input}}</p><p style="margin:5px 0 0;color:#71717a;font-size:11px;line-height:18px;">其中缓存 {{.Cache}}</p></td><td width="50%" valign="top" style="padding:16px 14px;"><p style="margin:0 0 8px;color:#71717a;font-size:12px;">输出</p><p style="margin:0;font-size:18px;line-height:26px;font-weight:600;overflow-wrap:anywhere;">{{.Output}}</p></td></tr>
</table>
{{if .Link}}<table role="presentation" cellspacing="0" cellpadding="0" style="margin-top:24px;"><tr><td bgcolor="#18181b" style="border-radius:8px;"><a href="{{.Link}}" style="display:inline-block;padding:13px 24px;color:#ffffff;text-decoration:none;font-size:14px;line-height:20px;font-weight:600;">查看详情 →</a></td></tr></table>{{end}}
<p style="margin:20px 0 0;font-size:11px;line-height:19px;color:#71717a;">截至本次提交结束的运行累计快照。缓存包含在输入中，费用为估算而非账单。</p>
</td></tr><tr><td style="padding:16px 4px 0;color:#71717a;font-size:11px;line-height:19px;overflow-wrap:anywhere;">详情需要登录；实验室内网部署可能需要 VPN。<br>提交编号：{{.SubmissionID}}</td></tr>
</table></td></tr></table></body></html>`))
