package http

import (
	"context"
	stdhttp "net/http"
	"testing"
)

func TestRedirectPolicy(t *testing.T) {
	t.Parallel()

	newReq := func(ctx context.Context) *stdhttp.Request {
		req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, "https://tracker.example/", nil)
		if err != nil {
			t.Fatalf("building request: %v", err)
		}
		return req
	}

	tests := []struct {
		name    string
		stamped bool
		via     int
		wantErr error
		wantNil bool
	}{
		{name: "stamped surfaces the redirect", stamped: true, via: 1, wantErr: stdhttp.ErrUseLastResponse},
		{name: "stamped wins even deep in a chain", stamped: true, via: 15, wantErr: stdhttp.ErrUseLastResponse},
		{name: "unstamped follows", via: 1, wantNil: true},
		{name: "unstamped follows at nine hops", via: 9, wantNil: true},
		{name: "unstamped stops after ten hops", via: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			if tt.stamped {
				ctx = WithNoRedirectFollow(ctx)
			}
			via := make([]*stdhttp.Request, tt.via)
			for i := range via {
				via[i] = newReq(context.Background())
			}
			err := RedirectPolicy(newReq(ctx), via)
			switch {
			case tt.wantErr != nil:
				if err != tt.wantErr { //nolint:errorlint // ErrUseLastResponse is matched by identity in net/http itself.
					t.Fatalf("RedirectPolicy() = %v, want %v", err, tt.wantErr)
				}
			case tt.wantNil:
				if err != nil {
					t.Fatalf("RedirectPolicy() = %v, want nil", err)
				}
			default:
				if err == nil {
					t.Fatal("RedirectPolicy() = nil, want hop-cap error")
				}
			}
		})
	}
}
