//go:build darwin

package computer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type darwinDriver struct {
	tempRoot string
	scaleX   float64
	scaleY   float64
}

func newPlatformDriver(home string) Driver {
	return &darwinDriver{tempRoot: filepath.Join(home, "computer-use", "tmp"), scaleX: 1, scaleY: 1}
}

func (d *darwinDriver) Platform() string { return "darwin" }
func (d *darwinDriver) Capabilities() []string {
	return []string{"screenshot", "mouse", "keyboard", "unicode_text", "app_inventory", "retina_coordinate_mapping"}
}

func (d *darwinDriver) Capture(ctx context.Context) (Screenshot, error) {
	if err := os.MkdirAll(d.tempRoot, 0o700); err != nil {
		return Screenshot{}, fmt.Errorf("create computer-use temporary directory: %w", err)
	}
	file, err := os.CreateTemp(d.tempRoot, "desktop-*.png")
	if err != nil {
		return Screenshot{}, fmt.Errorf("create screenshot target: %w", err)
	}
	path := file.Name()
	_ = file.Close()
	defer os.Remove(path)
	output, err := exec.CommandContext(ctx, "/usr/sbin/screencapture", "-x", "-m", "-t", "png", path).CombinedOutput()
	if err != nil {
		return Screenshot{}, fmt.Errorf("capture main display: %s", strings.TrimSpace(string(output)))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Screenshot{}, fmt.Errorf("read desktop screenshot: %w", err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Screenshot{}, fmt.Errorf("decode desktop screenshot: %w", err)
	}
	d.updateScale(ctx, cfg.Width, cfg.Height)
	shot := Screenshot{PNG: data, Geometry: Geometry{Width: cfg.Width, Height: cfg.Height}}
	if apps, appsErr := d.Apps(ctx); appsErr == nil {
		for i := range apps {
			if apps[i].Foreground {
				shot.Foreground = &apps[i]
				break
			}
		}
	}
	return shot, nil
}

func (d *darwinDriver) updateScale(ctx context.Context, pixelWidth, pixelHeight int) {
	const script = `ObjC.import('AppKit'); const frame=$.NSScreen.mainScreen.frame; JSON.stringify({width:Number(frame.size.width),height:Number(frame.size.height)})`
	output, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", script).Output()
	if err != nil {
		return
	}
	var points struct{ Width, Height float64 }
	if json.Unmarshal(output, &points) == nil && points.Width > 0 && points.Height > 0 {
		d.scaleX = float64(pixelWidth) / points.Width
		d.scaleY = float64(pixelHeight) / points.Height
	}
}

func (d *darwinDriver) Apps(ctx context.Context) ([]App, error) {
	const script = `const se=Application('System Events'); const ps=se.applicationProcesses.whose({backgroundOnly:false})(); JSON.stringify(ps.map(p=>({pid:p.unixId(),name:p.name(),foreground:p.frontmost()})))`
	output, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", script).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("list desktop applications: %s", strings.TrimSpace(string(output)))
	}
	var apps []App
	if err := json.Unmarshal(output, &apps); err != nil {
		return nil, fmt.Errorf("decode desktop application list: %w", err)
	}
	return apps, nil
}

func (d *darwinDriver) Act(ctx context.Context, action Action, geometry Geometry, allowSystemKeys bool) error {
	if action.Kind == "wait" {
		return waitContext(ctx, time.Duration(action.DurationMS)*time.Millisecond)
	}
	if action.Kind == "key" && !allowSystemKeys && isBlockedSystemKey(d.Platform(), action.Key) {
		return fmt.Errorf("system-level key combination is disabled by AGENTDOCK_COMPUTER_USE_ALLOW_SYSTEM_KEYS")
	}
	payload := struct {
		Action
		OriginX int     `json:"origin_x"`
		OriginY int     `json:"origin_y"`
		ScaleX  float64 `json:"scale_x"`
		ScaleY  float64 `json:"scale_y"`
	}{Action: action, OriginX: geometry.OriginX, OriginY: geometry.OriginY, ScaleX: d.scaleX, ScaleY: d.scaleY}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	output, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", darwinActionScript, "--", string(encoded)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("macOS desktop input failed: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

const darwinActionScript = `
ObjC.import('CoreGraphics');
function run(argv) {
  const a=JSON.parse(argv[0]);
  const sx=a.scale_x>0?a.scale_x:1, sy=a.scale_y>0?a.scale_y:1;
  const point=(x,y)=>$.CGPointMake(a.origin_x+x/sx,a.origin_y+y/sy);
  const buttons={left:0,right:1,middle:2};
  const types={left:{down:1,up:2,drag:6},right:{down:3,up:4,drag:7},middle:{down:25,up:26,drag:27}};
  function mouse(type,x,y,button,count) {
    const e=$.CGEventCreateMouseEvent(null,type,point(x,y),buttons[button]);
    if (count>1) $.CGEventSetIntegerValueField(e,1,count);
    $.CGEventPost(0,e); $.CFRelease(e);
  }
  if (a.action==='move') { mouse(5,a.x,a.y,'left',1); return; }
  if (a.action==='click') {
    for (let i=1;i<=a.click_count;i++) { mouse(types[a.button].down,a.x,a.y,a.button,i); mouse(types[a.button].up,a.x,a.y,a.button,i); }
    return;
  }
  if (a.action==='mouse_down') { mouse(types[a.button].down,a.x,a.y,a.button,1); return; }
  if (a.action==='mouse_up') { mouse(types[a.button].up,a.x,a.y,a.button,1); return; }
  if (a.action==='drag') {
    mouse(types[a.button].down,a.x,a.y,a.button,1);
    try {
      const steps=Math.max(2,Math.min(120,Math.ceil(a.duration_ms/8)));
      for(let i=1;i<=steps;i++) { const t=i/steps; mouse(types[a.button].drag,a.x+(a.to_x-a.x)*t,a.y+(a.to_y-a.y)*t,a.button,1); delay(a.duration_ms/steps/1000); }
    } finally {
      mouse(types[a.button].up,a.to_x,a.to_y,a.button,1);
    }
    return;
  }
  if (a.action==='scroll') {
    mouse(5,a.x,a.y,'left',1);
    const e=$.CGEventCreateScrollWheelEvent(null,0,2,-a.delta_y*40,-a.delta_x*40); $.CGEventPost(0,e); $.CFRelease(e); return;
  }
  const se=Application('System Events');
  if (a.action==='type') { se.keystroke(a.text); return; }
  if (a.action==='key') {
    const codes={return:36,enter:36,tab:48,space:49,delete:51,backspace:51,escape:53,esc:53,left:123,right:124,down:125,up:126,home:115,end:119,pageup:116,pagedown:121,f1:122,f2:120,f3:99,f4:118,f5:96,f6:97,f7:98,f8:100,f9:101,f10:109,f11:103,f12:111};
    for (const chord of a.key.trim().split(/\s+/)) {
      const parts=chord.toLowerCase().split('+'), key=parts.pop();
      const mods=parts.map(p=>({cmd:'command down',command:'command down',super:'command down',win:'command down',meta:'command down',ctrl:'control down',control:'control down',alt:'option down',option:'option down',shift:'shift down'}[p])).filter(Boolean);
      if (Object.prototype.hasOwnProperty.call(codes,key)) se.keyCode(codes[key],{using:mods}); else se.keystroke(key,{using:mods});
    }
    return;
  }
  throw new Error('unsupported action '+a.action);
}`
