package platform

// Coordinates below are physical pixels, including negative monitor origins.
// Sizes and spacing are declared once in logical pixels and scaled at the edge.
type compactGeometryRect struct{ Left, Top, Right, Bottom int32 }
type compactGeometrySize struct{ Width, Height int32 }

const (
	compactBaseDPI        = 96
	compactEntryWidth     = 26
	compactEntryHeight    = 52
	compactEntryBottomGap = 8
	compactPanelWidth     = 340
	compactPanelHeight    = 440
	compactPanelMinWidth  = 320
	compactPanelMinHeight = 420
	compactTaskRowHeight  = 26
)

func compactScale(value int32, dpi uint32) int32 {
	if dpi == 0 {
		dpi = compactBaseDPI
	}
	return int32((int64(value)*int64(dpi) + compactBaseDPI/2) / compactBaseDPI)
}
func compactClamp(value, low, high int32) int32 {
	if high < low {
		high = low
	}
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}
func (r compactGeometryRect) width() int32  { return r.Right - r.Left }
func (r compactGeometryRect) height() int32 { return r.Bottom - r.Top }
func compactVisibleBounds(monitor, work compactGeometryRect) compactGeometryRect {
	if monitor.width() <= 0 || monitor.height() <= 0 {
		return work
	}
	if work.width() <= 0 || work.height() <= 0 {
		return monitor
	}
	// rcWork only moves a side inward when reserved UI (such as a taskbar)
	// occupies that side. Otherwise the tab reaches the physical monitor edge.
	r := compactGeometryRect{
		Left: max(monitor.Left, work.Left), Top: max(monitor.Top, work.Top),
		Right: min(monitor.Right, work.Right), Bottom: min(monitor.Bottom, work.Bottom),
	}
	if r.width() <= 0 || r.height() <= 0 {
		return monitor
	}
	return r
}
func compactEntryDimensions(dpi uint32) compactGeometrySize {
	return compactGeometrySize{compactScale(compactEntryWidth, dpi), compactScale(compactEntryHeight, dpi)}
}
func compactDockGeometry(monitor, work compactGeometryRect, dpi uint32) compactGeometryRect {
	area := compactVisibleBounds(monitor, work)
	size := compactEntryDimensions(dpi)
	size.Width, size.Height = min(size.Width, area.width()), min(size.Height, area.height())
	x := area.Right - size.Width
	y := compactClamp(area.Bottom-size.Height-compactScale(compactEntryBottomGap, dpi), area.Top, area.Bottom-size.Height)
	return compactGeometryRect{x, y, x + size.Width, y + size.Height}
}
func compactPanelGeometry(area compactGeometryRect, requested compactGeometrySize, dpi uint32) compactGeometryRect {
	width, height := min(requested.Width, area.width()), min(requested.Height, area.height())
	gap := compactScale(8, dpi)
	x := compactClamp(area.Right-width-gap, area.Left, area.Right-width)
	y := compactClamp(area.Bottom-height-gap, area.Top, area.Bottom-height)
	return compactGeometryRect{x, y, x + width, y + height}
}

type compactViewAction uint8

const (
	compactViewOpenPanel compactViewAction = iota
	compactViewHidePanel
	compactViewShowEntry
)

type compactViewState struct{ PanelVisible bool }

func (state compactViewState) transition(action compactViewAction) compactViewState {
	switch action {
	case compactViewOpenPanel:
		state.PanelVisible = true
	case compactViewHidePanel:
		state.PanelVisible = false
	case compactViewShowEntry: // Existing editor stays open; never reveal a second surface.
	}
	return state
}
func (state compactViewState) entryVisible() bool { return !state.PanelVisible }

// Native control rectangles are returned in physical client coordinates.
type compactPanelLayout struct {
	Heading, Mode, Project, Body, Save, Restore, Refresh, Status, TodayHeading, Tasks, Close, Version compactGeometryRect
	TaskRowHeight                                                                                     int32
}

func compactPanelLayoutFor(width, height int32, dpi uint32) compactPanelLayout {
	s := func(n int32) int32 { return compactScale(n, dpi) }
	r := func(x, y, w, h int32) compactGeometryRect { return compactGeometryRect{x, y, x + w, y + h} }
	bodyHeight := s(90)
	if height > s(compactPanelHeight) {
		bodyHeight += (height - s(compactPanelHeight)) / 3
	}
	buttonY := s(70) + bodyHeight + s(8)
	statusY := buttonY + s(34)
	todayY := statusY + s(38)
	listY := todayY + s(26)
	footerY := height - s(22)
	return compactPanelLayout{
		Heading:       r(s(12), s(8), width-s(54), s(22)),
		Close:         r(width-s(38), s(5), s(28), s(28)),
		Mode:          r(s(10), s(36), s(104), s(200)),
		Project:       r(s(122), s(36), width-s(132), s(200)),
		Body:          r(s(10), s(70), width-s(20), bodyHeight),
		Save:          r(s(10), buttonY, s(100), s(28)),
		Restore:       r(s(118), buttonY, s(82), s(28)),
		Refresh:       r(s(208), buttonY, width-s(218), s(28)),
		Status:        r(s(12), statusY, width-s(24), s(32)),
		TodayHeading:  r(s(12), todayY, width-s(24), s(20)),
		Tasks:         r(s(10), listY, width-s(20), footerY-s(8)-listY),
		Version:       r(s(12), footerY, width-s(24), s(16)),
		TaskRowHeight: s(compactTaskRowHeight),
	}
}

func compactPanelMinimumClient(dpi uint32) compactGeometrySize {
	size := compactGeometrySize{compactScale(compactPanelMinWidth, dpi), compactScale(compactPanelMinHeight, dpi)}
	layout := compactPanelLayoutFor(size.Width, size.Height, dpi)
	// Independent rounding at 125% must still leave four complete rows plus
	// the native list border; scaling one logical height is insufficient.
	minimumList := 4*layout.TaskRowHeight + 2*compactScale(2, dpi)
	if deficit := minimumList - layout.Tasks.height(); deficit > 0 {
		size.Height += deficit
	}
	return size
}
