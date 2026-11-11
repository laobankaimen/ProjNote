package platform

import "testing"

func TestCompactSingleArrowFixedLowerRightAcrossMonitorsAndDPI(t *testing.T) {
	for _, c := range []struct {
		name          string
		monitor, work compactGeometryRect
	}{
		{"bottom taskbar", compactGeometryRect{0, 0, 1920, 1080}, compactGeometryRect{0, 0, 1920, 1040}},
		{"left taskbar", compactGeometryRect{0, 0, 1920, 1080}, compactGeometryRect{48, 0, 1920, 1080}},
		{"right taskbar", compactGeometryRect{-1920, -200, 0, 880}, compactGeometryRect{-1920, -200, -48, 880}},
		{"negative upper monitor", compactGeometryRect{-1600, -1200, 0, 0}, compactGeometryRect{-1600, -1160, 0, 0}},
		{"offset right monitor", compactGeometryRect{1920, 300, 3840, 1380}, compactGeometryRect{1920, 300, 3840, 1332}},
		{"desktop-wide workarea", compactGeometryRect{-1920, -200, 0, 880}, compactGeometryRect{-1920, -160, 1920, 880}},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, dpi := range []uint32{96, 120, 144, 192} {
				area := compactVisibleBounds(c.monitor, c.work)
				size := compactEntryDimensions(dpi)
				r := compactDockGeometry(c.monitor, c.work, dpi)
				if r.Right != area.Right || r.Bottom != area.Bottom-compactScale(compactEntryBottomGap, dpi) || r.width() != size.Width || r.height() != size.Height || r.Top < area.Top {
					t.Fatalf("fixed arrow dpi%d: %+v work%+v", dpi, r, area)
				}
				if size.Width < compactScale(22, dpi) || size.Width > compactScale(28, dpi) || size.Height < compactScale(44, dpi) {
					t.Fatal("single arrow click target", dpi, size)
				}
			}
		})
	}
}

func TestCompactViewOnlyOneVisibleSurfaceAndShowEntryPreservesEditor(t *testing.T) {
	state := compactViewState{}
	if !state.entryVisible() || state.PanelVisible {
		t.Fatal("default must only show an icon", state)
	}
	for i := 0; i < 20; i++ {
		state = state.transition(compactViewOpenPanel)
		if !state.PanelVisible || state.entryVisible() {
			t.Fatal("open must hide the icon", state)
		}
		state = state.transition(compactViewShowEntry)
		if !state.PanelVisible || state.entryVisible() {
			t.Fatal("ShowEntry exposed a second surface or closed the editor", state)
		}
		state = state.transition(compactViewHidePanel)
		if state.PanelVisible || !state.entryVisible() {
			t.Fatal("close must restore exactly the single icon", state)
		}
		state = state.transition(compactViewShowEntry)
		if state.PanelVisible || !state.entryVisible() {
			t.Fatal("ShowEntry opened editor", state)
		}
	}
}

func TestCompactPanelLayoutFourFullRowsAtDefaultMinimumAndDPI(t *testing.T) {
	for _, dpi := range []uint32{96, 120, 144, 192} {
		minimum := compactPanelMinimumClient(dpi)
		for _, size := range []compactGeometrySize{minimum,
			{compactScale(compactPanelWidth, dpi), compactScale(compactPanelHeight, dpi)},
			{compactScale(500, dpi), compactScale(620, dpi)},
		} {
			layout := compactPanelLayoutFor(size.Width, size.Height, dpi)
			if layout.Tasks.height() < 4*layout.TaskRowHeight+2*compactScale(2, dpi) {
				t.Fatalf("task rows clipped dpi%d %+v: list%d row%d", dpi, size, layout.Tasks.height(), layout.TaskRowHeight)
			}
			if layout.Body.height() < compactScale(90, dpi) || layout.Status.height() < compactScale(30, dpi) {
				t.Fatal("editor or feedback squeezed", dpi, layout)
			}
			for _, r := range []compactGeometryRect{layout.Heading, layout.Body, layout.Save, layout.Refresh, layout.Status, layout.TodayHeading, layout.Complete, layout.Tasks, layout.Close, layout.Full, layout.Version} {
				if r.Left < 0 || r.Top < 0 || r.Right > size.Width || r.Bottom > size.Height || r.width() <= 0 || r.height() <= 0 {
					t.Fatalf("control outside client dpi%d: %+v size%+v", dpi, r, size)
				}
			}
			if layout.Heading.Right > layout.Close.Left || layout.Close.Bottom > layout.Mode.Top || layout.Body.Bottom > layout.Save.Top || layout.Save.Bottom > layout.Status.Top || layout.Status.Bottom > layout.TodayHeading.Top || layout.TodayHeading.Bottom > layout.Tasks.Top || layout.Tasks.Bottom > layout.Full.Top {
				t.Fatal("native controls overlap", dpi, layout)
			}
			if layout.Save.Right > layout.Refresh.Left || layout.TodayHeading.Right > layout.Complete.Left || layout.Complete.Bottom > layout.Tasks.Top || layout.Full.Right > layout.Version.Left {
				t.Fatal("action controls overlap", dpi, layout)
			}
		}
	}
}

func TestCompactPanelPlacementFixedLowerRightAndSmallWorkarea(t *testing.T) {
	area := compactGeometryRect{-1920, -1080, 0, -40}
	for _, dpi := range []uint32{96, 120, 144, 192} {
		size := compactGeometrySize{compactScale(340, dpi), compactScale(440, dpi)}
		panel := compactPanelGeometry(area, size, dpi)
		if panel.Left < area.Left || panel.Top < area.Top || panel.Right != area.Right-compactScale(8, dpi) || panel.Bottom != area.Bottom-compactScale(8, dpi) || panel.width() != size.Width || panel.height() != size.Height {
			t.Fatal("fixed panel clipped or on wrong monitor", dpi, panel)
		}
	}
	tiny := compactGeometryRect{100, 200, 120, 240}
	panel := compactPanelGeometry(tiny, compactGeometrySize{800, 900}, 144)
	entry := compactDockGeometry(tiny, tiny, 144)
	if panel != tiny || entry != tiny {
		t.Fatal("reduced monitor must constrain both windows", panel, entry)
	}
}
