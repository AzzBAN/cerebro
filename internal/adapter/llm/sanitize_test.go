package llm

import "testing"

func TestStripReasoning(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"no tags", "Hello operator.", "Hello operator."},
		{
			name: "think block before answer",
			in:   "<think>The user typed Helo, a greeting. I should respond warmly.</think>\nHey there.",
			want: "Hey there.",
		},
		{
			name: "thinking tag uppercase",
			in:   "<THINKING>plan plan plan</THINKING>Answer.",
			want: "Answer.",
		},
		{
			name: "reasoning block",
			in:   "<reasoning>step 1\nstep 2</reasoning> Final.",
			want: "Final.",
		},
		{
			name: "multiline think",
			in:   "<think>\nline1\nline2\n</think>\n\nThe balance is 1000 USDT.",
			want: "The balance is 1000 USDT.",
		},
		{
			name: "dangling open tag (truncated)",
			in:   "Partial answer.<think>never closed because the stream",
			want: "Partial answer.",
		},
		{
			name: "tag in middle preserved-answer",
			in:   "Before.<think>hidden</think>After.",
			want: "Before.After.",
		},
		{
			name: "legitimate angle brackets preserved",
			in:   "Use the operator <symbol> like BTCUSDT.",
			want: "Use the operator <symbol> like BTCUSDT.",
		},
		{
			name: "answer only whitespace after strip",
			in:   "<think>only thoughts here</think>   ",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripReasoning(tt.in); got != tt.want {
				t.Errorf("stripReasoning(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
