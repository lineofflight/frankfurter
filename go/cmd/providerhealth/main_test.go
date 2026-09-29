package main

import (
	"context"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// Locks the staleness calibration: thresholds must catch genuinely frozen feeds (NBC at 10 missed) while tolerating
// normal lag (FBIL holidays at 4, T+1 at 1, monthly archives in arrears).

func missed(n int) *int { return &n }

func entry(key, cadence string, n *int) Entry {
	return Entry{Key: key, Name: key + " Bank", PublishCadence: cadence, PublishesMissed: n, EndDate: "2026-06-05"}
}

func keys(entries []Entry) []string {
	out := []string{}
	for _, e := range entries {
		out = append(out, e.Key)
	}
	return out
}

func TestFlaggedFlagsDailyProvidersAtOrAboveTheThreshold(t *testing.T) {
	got := keys(flagged([]Entry{entry("AT", "daily", missed(8)), entry("BELOW", "daily", missed(7))}))
	if !slices.Equal(got, []string{"AT"}) {
		t.Fatalf("got %v", got)
	}
}

func TestFlaggedToleratesNormalDailyLag(t *testing.T) {
	got := flagged([]Entry{entry("FBIL", "daily", missed(4)), entry("CBI", "daily", missed(2)),
		entry("BOJ", "daily", missed(1))})
	if len(got) != 0 {
		t.Fatalf("got %v", keys(got))
	}
}

func TestFlaggedFlagsGenuinelyFrozenDailyFeeds(t *testing.T) {
	if got := keys(flagged([]Entry{entry("NBC", "daily", missed(10))})); !slices.Equal(got, []string{"NBC"}) {
		t.Fatalf("got %v", got)
	}
}

func TestFlaggedFlagsWeeklyAndMonthlyAtTwoMissedBuckets(t *testing.T) {
	got := keys(flagged([]Entry{entry("W2", "weekly", missed(2)), entry("W1", "weekly", missed(1)),
		entry("M2", "monthly", missed(2)), entry("M1", "monthly", missed(1))}))
	slices.Sort(got)
	if !slices.Equal(got, []string{"M2", "W2"}) {
		t.Fatalf("got %v", got)
	}
}

func TestFlaggedNeverFlagsHistoricalOnlyProviders(t *testing.T) {
	if got := flagged([]Entry{entry("BBK", "", missed(9999))}); len(got) != 0 {
		t.Fatalf("got %v", keys(got))
	}
}

func TestFlaggedTreatsAMissingMissedCountAsZero(t *testing.T) {
	if got := flagged([]Entry{entry("X", "daily", nil)}); len(got) != 0 {
		t.Fatalf("got %v", keys(got))
	}
}

func TestFlaggedOrdersByMissedCountDescending(t *testing.T) {
	got := keys(flagged([]Entry{entry("LOW", "daily", missed(8)), entry("HIGH", "daily", missed(30))}))
	if !slices.Equal(got, []string{"HIGH", "LOW"}) {
		t.Fatalf("got %v", got)
	}
}

func withUnknown(e Entry, codes ...string) Entry {
	e.UnknownCurrencies = codes
	return e
}

func TestFlaggedFlagsAnUpToDateProviderWithUnknownCodes(t *testing.T) {
	e := withUnknown(entry("CBKKW", "daily", missed(0)), "ECS", "WAUA")
	if got := flagged([]Entry{e}); !reflect.DeepEqual(got, []Entry{e}) {
		t.Fatalf("got %+v", got)
	}
}

func TestFlaggedFlagsUnknownCodesFromHistoricalOnlyProviders(t *testing.T) {
	e := withUnknown(entry("BBK", "", nil), "ZZZ")
	if got := flagged([]Entry{e}); !reflect.DeepEqual(got, []Entry{e}) {
		t.Fatalf("got %+v", got)
	}
}

func TestRenderBodyNamesCodesAndRemediationWithoutClaimingStaleness(t *testing.T) {
	body := renderBody(withUnknown(entry("CBKKW", "daily", missed(0)), "ECS", "WAUA"), "2026-09-17")
	for _, s := range []string{"CBKKW", "ECS", "WAUA", "currency_patches.json"} {
		if !strings.Contains(body, s) {
			t.Errorf("body lacks %q", s)
		}
	}
	if strings.Contains(body, "has missed") {
		t.Error("body claims staleness")
	}
}

func TestRenderBodyReportsStalenessAndUnknownCodesInOneIssue(t *testing.T) {
	body := renderBody(withUnknown(entry("CBKKW", "daily", missed(10)), "ECS"), "2026-09-17")
	if !strings.Contains(body, "has missed") || !strings.Contains(body, "ECS") {
		t.Fatalf("body %s", body)
	}
}

func audit(t *testing.T, entries []Entry, open map[string]int) [][]string {
	t.Helper()
	var commands [][]string
	a := auditor{
		repo:       "lineofflight/frankfurter",
		today:      "2026-09-17",
		fetch:      func(context.Context) ([]Entry, error) { return entries, nil },
		openIssues: func(context.Context) (map[string]int, error) { return open, nil },
		gh: func(_ context.Context, args ...string) (string, error) {
			commands = append(commands, args)
			return "", nil
		},
	}
	if err := a.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	return commands
}

func TestAuditOpensAnIssueNamingTheProviderAndUnknownCurrency(t *testing.T) {
	commands := audit(t, []Entry{withUnknown(entry("CBKKW", "daily", missed(0)), "ECS")}, map[string]int{})
	if len(commands) != 1 || !slices.Equal(commands[0][:2], []string{"issue", "create"}) {
		t.Fatalf("commands %q", commands)
	}
	last := commands[0][len(commands[0])-1]
	if !strings.Contains(last, "CBKKW") || !strings.Contains(last, "ECS") {
		t.Fatalf("body %s", last)
	}
}

func TestAuditKeepsAnIssueOpenWhilePublishingRecoversButCodesRemainUnknown(t *testing.T) {
	commands := audit(t, []Entry{withUnknown(entry("CBKKW", "daily", missed(0)), "ECS")}, map[string]int{"CBKKW": 123})
	if len(commands) != 1 || !slices.Equal(commands[0][:3], []string{"issue", "edit", "123"}) {
		t.Fatalf("commands %q", commands)
	}
}

func TestAuditClosesTheIssueOnceBothChecksRecover(t *testing.T) {
	commands := audit(t, []Entry{withUnknown(entry("CBKKW", "daily", missed(0)))}, map[string]int{"CBKKW": 123})
	if len(commands) != 2 || !slices.Equal(commands[0][:3], []string{"issue", "comment", "123"}) ||
		!slices.Equal(commands[1][:3], []string{"issue", "close", "123"}) {
		t.Fatalf("commands %q", commands)
	}
}

func TestRenderBodyEmbedsAPerProviderMarkerAndTheProvidersStats(t *testing.T) {
	body := renderBody(entry("NBC", "daily", missed(10)), "2026-06-22")
	for _, s := range []string{"<!-- provider-health: NBC -->", "NBC Bank", "| daily | 2026-06-05 | 10 |"} {
		if !strings.Contains(body, s) {
			t.Errorf("body lacks %q", s)
		}
	}
}

func TestRenderBodyUsesNoEmDash(t *testing.T) {
	if strings.Contains(renderBody(entry("NBC", "daily", missed(10)), "2026-06-22"), "—") {
		t.Fatal("em dash in body")
	}
}

// testdata/body.txt is Ruby's render_body for the same entry.
func TestRenderBodyMatchesRuby(t *testing.T) {
	want, err := os.ReadFile("testdata/body.txt")
	if err != nil {
		t.Fatal(err)
	}
	e := withUnknown(entry("CBKKW", "daily", missed(10)), "ECS", "WAUA")
	if got := renderBody(e, "2026-09-17"); got != string(want) {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestDryRunPrintsTheDecisionAndBodiesWithoutTouchingIssues(t *testing.T) {
	stale := entry("NBC", "daily", missed(10))
	unknown := withUnknown(entry("CBKKW", "daily", missed(0)), "ECS")
	var stderr strings.Builder
	a := auditor{
		today:  "2026-09-17",
		dryRun: true,
		stderr: &stderr,
		fetch: func(context.Context) ([]Entry, error) {
			return []Entry{unknown, entry("OK", "daily", missed(1)), stale}, nil
		},
		openIssues: func(context.Context) (map[string]int, error) {
			t.Error("dry run listed issues")
			return nil, nil
		},
		gh: func(context.Context, ...string) (string, error) {
			t.Error("dry run ran gh")
			return "", nil
		},
	}
	if err := a.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := `DRY_RUN flagged: ["NBC", "CBKKW"]` + "\n" + renderBody(stale, a.today) + renderBody(unknown, a.today)
	if stderr.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", stderr.String(), want)
	}
}

func TestMarkerRoundTrips(t *testing.T) {
	m := markerRe.FindStringSubmatch("intro\n" + marker("NBC") + "\n")
	if m == nil || m[1] != "NBC" {
		t.Fatalf("got %v", m)
	}
}
