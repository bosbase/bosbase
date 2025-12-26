package core

import "testing"

func TestGenerateSequentialLogId(t *testing.T) {
	t.Parallel()

	id1 := generateSequentialLogId()
	id2 := generateSequentialLogId()

	if id1 == id2 {
		t.Fatalf("expected unique sequential ids, got identical values %q", id1)
	}

	if id2 <= id1 {
		t.Fatalf("expected id2 (%s) to be greater than id1 (%s)", id2, id1)
	}
}
