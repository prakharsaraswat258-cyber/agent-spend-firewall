package notify

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/decide"
)

// Toast is the Windows desktop channel: a native notification whose click (or
// "Open decision" button) opens the dashboard card. Windows notifications can't
// post an answer back, so the click-through is the way to answer.
// It needs no extra install: it uses Windows PowerShell's WinRT access.
type Toast struct {
	URL string // dashboard address, e.g. http://127.0.0.1:4546/dashboard
	// Run shows the toast; nil uses PowerShell. Replaced in tests.
	Run func(script string) error
}

// powershellAppID is the AppUserModelID Windows PowerShell registers, so toasts
// show without installing a shortcut of our own.
const powershellAppID = `{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe`

func (t Toast) Name() string { return "desktop" }

func (t Toast) Card(c decide.Card) error {
	return t.show("Pitstop: your agent is paused", c.Text()+"Continue or stop: click to open the decision.", true)
}

func (t Toast) Remind(c decide.Card) error {
	return t.show("Pitstop: still waiting for you", c.Text()+"No answer means the run stops and your work is saved.", true)
}

func (t Toast) Resolved(c decide.Card, a decide.Answer, outcome string) error {
	if !a.TimedOut {
		return nil // you answered; no need to tell you
	}
	return t.show("Pitstop: run "+c.Run+" stopped", outcome, false)
}

func (t Toast) show(title, body string, button bool) error {
	script := ToastScript(ToastXML(title, body, t.URL, button))
	if t.Run != nil {
		return t.Run(script)
	}
	if runtime.GOOS != "windows" {
		return errors.New("windows notifications need Windows")
	}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", "-")
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

func esc(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// ToastXML builds the notification. Clicking it opens url in the browser.
func ToastXML(title, body, url string, button bool) string {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	var b strings.Builder
	fmt.Fprintf(&b, `<toast activationType="protocol" launch="%s" scenario="reminder"><visual><binding template="ToastGeneric"><text>%s</text>`, esc(url), esc(title))
	// ToastGeneric shows up to 3 text lines; join the rest so nothing is cut.
	if len(lines) > 2 {
		lines = []string{lines[0], strings.Join(lines[1:], " ")}
	}
	for _, l := range lines {
		fmt.Fprintf(&b, `<text>%s</text>`, esc(l))
	}
	b.WriteString(`</binding></visual>`)
	if button {
		fmt.Fprintf(&b, `<actions><action content="Open decision" activationType="protocol" arguments="%s"/></actions>`, esc(url))
	}
	b.WriteString(`</toast>`)
	return b.String()
}

// ToastScript is the PowerShell that shows one toast. The XML goes in a
// single-quoted here-string, so nothing in it is expanded by PowerShell.
func ToastScript(toastXML string) string {
	toastXML = strings.ReplaceAll(toastXML, "\n'@", "\n '@") // cannot end the here-string early
	return `$ErrorActionPreference = 'Stop'
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] | Out-Null
$x = New-Object Windows.Data.Xml.Dom.XmlDocument
$x.LoadXml(@'
` + toastXML + `
'@)
$t = [Windows.UI.Notifications.ToastNotification]::new($x)
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('` + powershellAppID + `').Show($t)
`
}
