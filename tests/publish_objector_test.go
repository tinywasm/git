package git_test

import (
	"github.com/tinywasm/git"
	"testing"
)

type fakeObjector struct {
	action git.PublishAction
	reason string
}

func (f fakeObjector) ObjectsToPublish(ctx git.PublishContext) (git.PublishAction, string) {
	return f.action, f.reason
}

func TestResolvePublishAction(t *testing.T) {
	tests := []struct {
		name       string
		objectors  []git.PublishObjector
		wantAction git.PublishAction
		wantReason string
	}{
		{
			name:       "none",
			objectors:  nil,
			wantAction: git.ActionNone,
			wantReason: "",
		},
		{
			name: "single skip",
			objectors: []git.PublishObjector{
				fakeObjector{git.ActionSkip, "skip it"},
			},
			wantAction: git.ActionSkip,
			wantReason: "skip it",
		},
		{
			name: "skip beats deps",
			objectors: []git.PublishObjector{
				fakeObjector{git.ActionDepsOnly, "deps"},
				fakeObjector{git.ActionSkip, "skip"},
			},
			wantAction: git.ActionSkip,
			wantReason: "skip",
		},
		{
			name: "deps beats none",
			objectors: []git.PublishObjector{
				fakeObjector{git.ActionNone, ""},
				fakeObjector{git.ActionDepsOnly, "deps"},
			},
			wantAction: git.ActionDepsOnly,
			wantReason: "deps",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action, reason := git.ResolvePublishAction(tt.objectors, git.PublishContext{})
			if action != tt.wantAction {
				t.Errorf("action = %v, want %v", action, tt.wantAction)
			}
			if reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", reason, tt.wantReason)
			}
		})
	}
}
