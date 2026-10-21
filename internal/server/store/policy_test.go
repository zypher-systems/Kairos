package store

import "testing"

func TestDecideConcurrency(t *testing.T) {
	cases := []struct {
		policy  string
		trigger string
		active  int
		want    ConcurrencyDecision
	}{
		{"allow", TriggerSchedule, 3, DecisionEnqueue},
		{"", TriggerSchedule, 2, DecisionEnqueue},
		{"forbid", TriggerSchedule, 0, DecisionEnqueue},
		{"forbid", TriggerSchedule, 1, DecisionSkip},
		{"replace", TriggerManual, 2, DecisionReplace},
		{"forbid", TriggerRetry, 1, DecisionSkip},
		{"forbid", TriggerWorkflow, 1, DecisionSkip},
		{"forbid", TriggerFile, 1, DecisionSkip},
		{"unknown", TriggerSchedule, 1, DecisionEnqueue},
	}
	for _, tc := range cases {
		got := DecideConcurrency(tc.policy, tc.trigger, tc.active)
		if got != tc.want {
			t.Errorf("DecideConcurrency(%q,%q,%d)=%s want %s",
				tc.policy, tc.trigger, tc.active, got, tc.want)
		}
	}
}
