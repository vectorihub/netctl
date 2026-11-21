package sim

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// TextOptions tune the text report.
type TextOptions struct {
	// Title names the network in the header, for example "three-site". It is
	// left out of the header when empty.
	Title string
	// Verbose lists every flow. Without it the report lists only the flows that
	// fail the gate or pass with a warning, so a report of 250,000 flows that
	// all passed stays short.
	Verbose bool
}

// alwaysListed are the verdicts the summary shows even when no flow has them, so
// a reader sees the zeros that matter (no loops, no black holes) rather than
// having to notice an absent line.
var alwaysListed = map[Verdict]bool{
	VerdictOK: true, VerdictAsymmetric: true, VerdictNoReturn: true, VerdictBlackHole: true,
	VerdictLoop: true, VerdictLinkDown: true, VerdictUnreachableExpected: true,
}

// Text writes the report as readable text:
//
//	SIMULATION three-site · scenario direct-link-down · 16 flows
//	  OK                         13
//	  ASYMMETRIC                  0
//	  ...
//	PASS
//
// followed by one block for each flow that fails the gate or warns (every flow
// with Verbose), showing the forward and return journeys link by link and
// whether they are symmetric. The summary lists the verdicts a reader most wants
// to see zero of even when they are zero, and any other verdict that occurs. The
// text is the same for the same report, so it can be kept as a golden file.
//
// Example: a report whose one flow builder-1 to dgx-spark was delivered both ways
// ends "PASS".
func (report Report) Text(opts TextOptions) string {
	return reportText(report, opts, "", nil)
}

// Text writes the scenario's report as readable text, with the scenario named in
// the header, then the expectations that were not met and the pairs of nodes the
// scenario cut off. The scenario passes only if the flows pass and every
// expectation is met.
//
// Example: a scenario whose expectation said OK for a flow that was BLACK_HOLE
// ends "FAIL" and lists that flow after the summary.
func (result ScenarioResult) Text(opts TextOptions) string {
	return reportText(result.Report, opts, result.Name, &result)
}

// reportText builds the text of a report. scenario, when it is not nil, adds
// the scenario's name, mismatches and cut-off pairs.
func reportText(report Report, opts TextOptions, name string, scenario *ScenarioResult) string {
	var out strings.Builder
	header := []string{"SIMULATION"}
	if opts.Title != "" {
		header[0] += " " + opts.Title
	}
	if name != "" {
		header = append(header, "scenario "+name)
	}
	header = append(header, plural(report.Summary.Total, "flow"))
	fmt.Fprintln(&out, strings.Join(header, " · "))
	for _, verdict := range verdictOrder {
		count := report.Summary.Count(verdict)
		if count > 0 || alwaysListed[verdict] {
			fmt.Fprintf(&out, "  %-24s%5d\n", verdict, count)
		}
	}
	passed := report.Passed
	if scenario != nil {
		passed = scenario.Passed
	}
	status := "PASS"
	switch {
	case !passed && scenario != nil && report.Passed:
		status = fmt.Sprintf("FAIL (%s not met)", plural(len(scenario.Mismatches), "expectation"))
	case !passed:
		status = fmt.Sprintf("FAIL (%d failed)", report.Summary.Failed)
	case report.Summary.Warnings > 0:
		status = fmt.Sprintf("PASS (%s)", plural(report.Summary.Warnings, "warning"))
	}
	fmt.Fprintln(&out, status)
	if scenario != nil {
		for _, mismatch := range scenario.Mismatches {
			fmt.Fprintf(&out, "\nEXPECTATION NOT MET: %s\n", mismatch.Detail)
		}
		for _, pair := range scenario.Unreachable {
			fmt.Fprintf(&out, "\nNO PATH: %s → %s (%s)\n", pair.From, pair.To, pair.Reason)
		}
	}
	for _, result := range report.Flows {
		if opts.Verbose || result.Verdict.FailsGate() || result.Verdict.Warning() {
			out.WriteString("\n")
			out.WriteString(flowText(result))
		}
	}
	return out.String()
}

// plural writes a count and a noun, adding an s unless the count is one: "1 flow",
// "16 flows".
func plural(count int, noun string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

// flowText writes one flow's block: a heading with the verdict, the forward and
// return journeys aligned in columns, whether they are symmetric, and any
// problems and notes.
func flowText(result FlowResult) string {
	var out strings.Builder
	flow := result.Flow
	fmt.Fprintf(&out, "%s (%s → %s) %s\n", flow.Label, flow.Src, flow.Dst, result.Verdict)
	forward := journeyText(result.Forward)
	ret := "not sent: the forward packet was not delivered"
	retEnd := ""
	if result.Return != nil {
		ret = journeyText(*result.Return)
		retEnd = endText(*result.Return)
	} else if flow.Negative() {
		ret = "not sent: a negative flow has no reply"
	}
	width := utf8.RuneCountInString(forward)
	if result.Return != nil && utf8.RuneCountInString(ret) > width {
		width = utf8.RuneCountInString(ret)
	}
	fmt.Fprintf(&out, "  fwd: %s  %s\n", pad(forward, width), endText(result.Forward))
	if result.Return != nil {
		fmt.Fprintf(&out, "  ret: %s  %s\n", pad(ret, width), retEnd)
		fmt.Fprintf(&out, "  symmetric: %s\n", yesNo(result.Symmetric))
	} else {
		fmt.Fprintf(&out, "  ret: %s\n", ret)
	}
	for _, problem := range result.Problems {
		fmt.Fprintf(&out, "  problem: %s\n", problem.Detail)
	}
	if flow.Note != "" {
		fmt.Fprintf(&out, "  note: %s\n", flow.Note)
	}
	return out.String()
}

// journeyText writes a trace's path as "a ─[link]→ b ─[link]→ c": every node it
// was at, with the link each forwarding hop crossed.
func journeyText(trace Trace) string {
	var out strings.Builder
	for i, hop := range trace.Hops {
		if i > 0 {
			out.WriteString(" ")
		}
		out.WriteString(string(hop.Node))
		if hop.Result == Forwarded && hop.Route != nil {
			fmt.Fprintf(&out, " ─[%s]→", hop.Route.Via)
		}
	}
	return out.String()
}

// endText writes how a trace ended and how many links it crossed, for example
// "DELIVERED (2 hops)".
func endText(trace Trace) string {
	return fmt.Sprintf("%s (%s)", trace.Final, plural(trace.Crossings(), "hop"))
}

// pad pads text with spaces on the right to the width in characters, not bytes,
// because the arrows are wider than a byte.
func pad(text string, width int) string {
	if gap := width - utf8.RuneCountInString(text); gap > 0 {
		return text + strings.Repeat(" ", gap)
	}
	return text
}

// yesNo writes a bool as "yes" or "no".
func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

// JSON writes the report as indented JSON with a final newline. The keys come out
// in a fixed order (the order of the struct fields), flows are sorted by label,
// and nothing in a report is a map, so the same report always gives the same
// bytes: suitable for a golden file and for comparing two runs. It returns an
// error rather than write a flow whose verdict or hop result is unknown.
//
// Example: two runs of the same scenario give byte-identical JSON.
func (report Report) JSON() ([]byte, error) { return marshalStable(report) }

// JSON writes the scenario result, its report included, as stable JSON in the
// same way as Report.JSON.
//
// Example: the result of the scenario "hub-down" starts {"name": "hub-down".
func (result ScenarioResult) JSON() ([]byte, error) { return marshalStable(result) }

// marshalStable writes value as two-space-indented JSON ending in a newline.
func marshalStable(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("sim: writing the report: %w", err)
	}
	return append(data, '\n'), nil
}
