package main

import "testing"

func TestEnvOr(t *testing.T) {
	t.Setenv("OMIXOR_TEST_X", "")
	if envOr("OMIXOR_TEST_X", "d") != "d" {
		t.Fatal("沒設要用預設值")
	}
	t.Setenv("OMIXOR_TEST_X", "v")
	if envOr("OMIXOR_TEST_X", "d") != "v" {
		t.Fatal("有設要用設定值")
	}
}
