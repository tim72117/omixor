package core

import (
	"strings"
	"testing"

	"omixor-core/model"
)

func TestRemoteExternalComments(t *testing.T) {
	in := []model.Comment{
		{ID: "jira-1", Author: "王小明", Text: "好"},
		{ID: "c-2", Author: "usr_x", Text: "本機"},
	}
	out := remoteExternalComments(in)
	if out[0].Author != "" || !strings.HasPrefix(out[0].Text, "【Jira:王小明】\n") || !strings.HasSuffix(out[0].Text, "好") {
		t.Fatalf("jira 留言應署名本人並把人名放內文: %+v", out[0])
	}
	if out[1] != in[1] {
		t.Fatalf("本機留言不該改: %+v", out[1])
	}
	if in[0].Author != "王小明" || in[0].Text != "好" {
		t.Fatal("不該改動傳入的 slice")
	}
	if remoteExternalAuthor("王小明") != "" {
		t.Fatal("遠端一律署名本人")
	}
}
