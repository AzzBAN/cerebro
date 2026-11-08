package cryptopanic

import (
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func TestBrowser_Lifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in short mode")
	}
	b := NewBrowser(10 * time.Second)
	defer b.Close()

	if err := b.ensureBrowser(); err != nil {
		t.Skipf("skipping: chrome not available: %v", err)
	}

	// First tab
	tabCtx1, tabCancel1 := chromedp.NewContext(b.browserCtx)
	var res1 int
	if err := chromedp.Run(tabCtx1, chromedp.Evaluate(`1 + 1`, &res1)); err != nil {
		tabCancel1()
		t.Fatalf("tab 1 run: %v", err)
	}
	if res1 != 2 {
		tabCancel1()
		t.Fatalf("expected 2, got %d", res1)
	}
	tabCancel1()

	// Verify browser process remains alive
	if b.browserCtx == nil || b.browserCtx.Err() != nil {
		t.Fatal("browserCtx was killed when tab 1 closed")
	}

	// Second tab on same browser
	tabCtx2, tabCancel2 := chromedp.NewContext(b.browserCtx)
	defer tabCancel2()
	var res2 int
	if err := chromedp.Run(tabCtx2, chromedp.Evaluate(`2 + 2`, &res2)); err != nil {
		t.Fatalf("tab 2 run: %v", err)
	}
	if res2 != 4 {
		t.Fatalf("expected 4, got %d", res2)
	}
}
