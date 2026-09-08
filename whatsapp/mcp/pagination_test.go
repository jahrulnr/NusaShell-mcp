package main

import "testing"

func TestClampPagination(t *testing.T) {
	cases := []struct {
		name                  string
		limit, offset         int
		wantLimit, wantOffset int
	}{
		{name: "negative", limit: -10, offset: -4, wantLimit: 1, wantOffset: 0},
		{name: "zero", limit: 0, offset: 0, wantLimit: 1, wantOffset: 0},
		{name: "upper bound", limit: 999, offset: 12, wantLimit: 200, wantOffset: 12},
		{name: "valid", limit: 25, offset: 3, wantLimit: 25, wantOffset: 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotLimit, gotOffset := clampPagination(tc.limit, tc.offset)
			if gotLimit != tc.wantLimit || gotOffset != tc.wantOffset {
				t.Fatalf("clampPagination(%d, %d) = (%d, %d), want (%d, %d)", tc.limit, tc.offset, gotLimit, gotOffset, tc.wantLimit, tc.wantOffset)
			}
		})
	}
}
