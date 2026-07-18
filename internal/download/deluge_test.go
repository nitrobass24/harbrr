package download

import (
	"context"
	"errors"
	"testing"

	"github.com/autobrr/go-deluge"

	"github.com/autobrr/harbrr/internal/domain"
)

// delugeFake implements delugeClient directly — go-deluge's raw rencode-over-TLS
// socket can't be pointed at an httptest server, so this fake is what proves
// delugeDriver's payload routing, option mapping, and label create-on-miss
// dance are correct.
type delugeFake struct {
	connectErr error
	daemonVer  string
	daemonErr  error

	addHash  string
	addErr   error
	lastAdd  string // "magnet" | "url" | "file"
	lastArg  string // uri/url/name
	lastB64  string
	lastOpts *deluge.Options

	setLabelErrs []error // consumed in call order; nil once exhausted
	setLabelCall int
	setLabelSeen []struct{ hash, label string }
	addLabelErr  error
	addLabelSeen []string

	closed bool
}

func (f *delugeFake) Connect(context.Context) error { return f.connectErr }
func (f *delugeFake) Close() error                  { f.closed = true; return nil }

func (f *delugeFake) DaemonVersion(context.Context) (string, error) { return f.daemonVer, f.daemonErr }

func (f *delugeFake) AddTorrentMagnet(_ context.Context, uri string, opts *deluge.Options) (string, error) {
	f.lastAdd, f.lastArg, f.lastOpts = "magnet", uri, opts
	return f.addHash, f.addErr
}

func (f *delugeFake) AddTorrentURL(_ context.Context, url string, opts *deluge.Options) (string, error) {
	f.lastAdd, f.lastArg, f.lastOpts = "url", url, opts
	return f.addHash, f.addErr
}

func (f *delugeFake) AddTorrentFile(_ context.Context, name, contentBase64 string, opts *deluge.Options) (string, error) {
	f.lastAdd, f.lastArg, f.lastB64, f.lastOpts = "file", name, contentBase64, opts
	return f.addHash, f.addErr
}

func (f *delugeFake) SetTorrentLabel(_ context.Context, hash, label string) error {
	f.setLabelSeen = append(f.setLabelSeen, struct{ hash, label string }{hash, label})
	if f.setLabelCall < len(f.setLabelErrs) {
		err := f.setLabelErrs[f.setLabelCall]
		f.setLabelCall++
		return err
	}
	return nil
}

func (f *delugeFake) AddLabel(_ context.Context, label string) error {
	f.addLabelSeen = append(f.addLabelSeen, label)
	return f.addLabelErr
}

func newDelugeDriver(fake *delugeFake, settings domain.DelugeSettings) *delugeDriver {
	return &delugeDriver{cli: fake, settings: settings}
}

func TestDelugeTest_OK(t *testing.T) {
	t.Parallel()
	fake := &delugeFake{daemonVer: "2.1.1"}
	drv := newDelugeDriver(fake, domain.DelugeSettings{})

	if err := drv.Test(context.Background()); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if !fake.closed {
		t.Fatal("Test: expected Close to be called")
	}
}

func TestDelugeTest_ConnectError(t *testing.T) {
	t.Parallel()
	fake := &delugeFake{connectErr: errors.New("dial tcp: connection refused")}
	drv := newDelugeDriver(fake, domain.DelugeSettings{})

	if err := drv.Test(context.Background()); err == nil {
		t.Fatal("expected a connect error")
	}
}

func TestDelugeAdd_ViaMagnet(t *testing.T) {
	t.Parallel()
	fake := &delugeFake{addHash: "abc123"}
	drv := newDelugeDriver(fake, domain.DelugeSettings{})

	uri := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: uri}, AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if fake.lastAdd != "magnet" || fake.lastArg != uri {
		t.Fatalf("lastAdd = %q lastArg = %q, want magnet/%s", fake.lastAdd, fake.lastArg, uri)
	}
}

func TestDelugeAdd_ViaURL(t *testing.T) {
	t.Parallel()
	fake := &delugeFake{addHash: "abc123"}
	drv := newDelugeDriver(fake, domain.DelugeSettings{})

	url := "http://tracker.example/dl?token=sealed"
	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: url}, AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if fake.lastAdd != "url" || fake.lastArg != url {
		t.Fatalf("lastAdd = %q lastArg = %q, want url/%s", fake.lastAdd, fake.lastArg, url)
	}
}

func TestDelugeAdd_ViaBytes(t *testing.T) {
	t.Parallel()
	fake := &delugeFake{addHash: "abc123"}
	drv := newDelugeDriver(fake, domain.DelugeSettings{})

	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, Bytes: []byte("d8:announce...e"), Name: "test.torrent"}, AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if fake.lastAdd != "file" || fake.lastArg != "test.torrent" {
		t.Fatalf("lastAdd = %q lastArg = %q, want file/test.torrent", fake.lastAdd, fake.lastArg)
	}
	if fake.lastB64 != "ZDg6YW5ub3VuY2UuLi5l" {
		t.Fatalf("lastB64 = %q, want base64 of the torrent bytes", fake.lastB64)
	}
}

func TestDelugeAdd_OptionMapping(t *testing.T) {
	t.Parallel()
	fake := &delugeFake{addHash: "abc123"}
	drv := newDelugeDriver(fake, domain.DelugeSettings{DownloadDir: "/downloads/deluge", StartPaused: true})

	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: "magnet:?xt=urn:btih:x"}, AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if fake.lastOpts.AddPaused == nil || !*fake.lastOpts.AddPaused {
		t.Fatalf("AddPaused = %v, want true (from settings.StartPaused)", fake.lastOpts.AddPaused)
	}
	if fake.lastOpts.DownloadLocation == nil || *fake.lastOpts.DownloadLocation != "/downloads/deluge" {
		t.Fatalf("DownloadLocation = %v, want /downloads/deluge", fake.lastOpts.DownloadLocation)
	}
	// no-hit-and-run: never a ratio/removal option.
	if fake.lastOpts.StopAtRatio != nil || fake.lastOpts.RemoveAtRatio != nil {
		t.Fatalf("StopAtRatio/RemoveAtRatio set: %v / %v, want nil", fake.lastOpts.StopAtRatio, fake.lastOpts.RemoveAtRatio)
	}
}

func TestDelugeAdd_LabelFallbackToSettings(t *testing.T) {
	t.Parallel()
	fake := &delugeFake{addHash: "abc123"}
	drv := newDelugeDriver(fake, domain.DelugeSettings{Label: "from-settings"})

	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: "magnet:?xt=urn:btih:x"}, AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(fake.setLabelSeen) != 1 || fake.setLabelSeen[0].label != "from-settings" {
		t.Fatalf("setLabelSeen = %v, want one call with label from-settings", fake.setLabelSeen)
	}
}

func TestDelugeAdd_LabelUnknownRetry(t *testing.T) {
	t.Parallel()
	fake := &delugeFake{
		addHash:      "abc123",
		setLabelErrs: []error{deluge.RPCError{ExceptionMessage: "Unknown Label"}},
	}
	drv := newDelugeDriver(fake, domain.DelugeSettings{})

	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: "magnet:?xt=urn:btih:x"}, AddOptions{Category: "tv-sonarr"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(fake.addLabelSeen) != 1 || fake.addLabelSeen[0] != "tv-sonarr" {
		t.Fatalf("addLabelSeen = %v, want one call with tv-sonarr", fake.addLabelSeen)
	}
	if len(fake.setLabelSeen) != 2 {
		t.Fatalf("setLabelSeen calls = %d, want 2 (initial miss + retry)", len(fake.setLabelSeen))
	}
}

func TestDelugeAdd_LabelPluginDisabled(t *testing.T) {
	t.Parallel()
	fake := &delugeFake{addHash: "abc123", setLabelErrs: []error{errDelugeLabelPluginDisabled}}
	drv := newDelugeDriver(fake, domain.DelugeSettings{})

	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: "magnet:?xt=urn:btih:x"}, AddOptions{Category: "tv-sonarr"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(fake.addLabelSeen) != 0 {
		t.Fatalf("addLabelSeen = %v, want no AddLabel call when the plugin is disabled", fake.addLabelSeen)
	}
}

func TestDelugeAdd_NoCategoryOrLabelSkipsLabeling(t *testing.T) {
	t.Parallel()
	fake := &delugeFake{addHash: "abc123"}
	drv := newDelugeDriver(fake, domain.DelugeSettings{})

	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: "magnet:?xt=urn:btih:x"}, AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(fake.setLabelSeen) != 0 {
		t.Fatalf("setLabelSeen = %v, want no calls with no category/label", fake.setLabelSeen)
	}
}

func TestDelugeAdd_UsenetUnsupported(t *testing.T) {
	t.Parallel()
	fake := &delugeFake{}
	drv := newDelugeDriver(fake, domain.DelugeSettings{})

	err := drv.Add(context.Background(), Payload{Protocol: ProtocolUsenet, URL: "https://example.com/release.nzb"}, AddOptions{})
	if !errors.Is(err, ErrUnsupportedProtocol) {
		t.Fatalf("Add(usenet) error = %v, want ErrUnsupportedProtocol", err)
	}
}

func TestNewDeluge_HostParsing(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		host    string
		wantErr bool
	}{
		{name: "valid host:port", host: "localhost:58846", wantErr: false},
		{name: "missing port", host: "localhost", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := newDeluge(domain.DownloadClient{Host: tt.host}, "secret", nil)
			if (err != nil) != tt.wantErr {
				t.Fatalf("newDeluge(%q) error = %v, wantErr %v", tt.host, err, tt.wantErr)
			}
		})
	}
}
