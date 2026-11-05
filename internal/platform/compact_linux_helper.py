# Native GTK widgets. All business HTTP is dispatched by the parent Go process.
import json
import os
import sys
import gi

gi.require_version("Gtk", "3.0")
gi.require_version("Gdk", "3.0")
from gi.repository import Gtk, Gdk, GLib

ok, _ = Gtk.init_check(sys.argv)
if not ok:
    raise SystemExit("GTK无法连接显示会话；不会用浏览器替代原生便签")

state = {"seq": 0, "pending": {}, "loading": True, "version": 0,
         "dirty": False, "draft_busy": False, "saving": False,
         "conflict": False, "debounce": 0, "suppress": False,
         "first_load": True, "auto_project": False, "project_items": None, "task_items": [],
         "dock_area": None, "settle_dock": 0, "entry_ready": False}


def request(action, payload=None, callback=None):
    state["seq"] += 1
    seq = state["seq"]
    if callback:
        state["pending"][seq] = callback
    print(json.dumps({"seq": seq, "action": action, "payload": payload or {}}, ensure_ascii=False), flush=True)


def usable_area(geometry, workarea):
    # X11 can report one desktop-wide workarea. Intersect it with this monitor
    # to keep its real edges without covering a panel/taskbar reservation.
    x, y = max(geometry.x, workarea.x), max(geometry.y, workarea.y)
    right = min(geometry.x + geometry.width, workarea.x + workarea.width)
    bottom = min(geometry.y + geometry.height, workarea.y + workarea.height)
    if right <= x or bottom <= y:
        return geometry
    result = Gdk.Rectangle()
    result.x, result.y, result.width, result.height = x, y, right - x, bottom - y
    return result


def area_for(window, position=None):
    display = Gdk.Display.get_default()
    x, y = position if position is not None else window.get_position()
    width, height = window.get_size()
    monitor = (display.get_monitor_at_point(x + width // 2, y + height // 2)
               or display.get_primary_monitor() or display.get_monitor(0))
    return usable_area(monitor.get_geometry(), monitor.get_workarea())


def dock_position(area, width, height):
    # One fixed anchor. There is deliberately no drag position or side state.
    return area.x + max(0, area.width - width), area.y + max(0, area.height - height - 8)


css = Gtk.CssProvider()
css.load_from_data(b"""
#compact-panel { background: #f3f6fa; color: #24334a; font: 10pt 'Noto Sans CJK SC'; }
#compact-panel button, #compact-panel combobox button { border-radius: 5px; border: 1px solid #dfe5ee; background-image: none; background-color: #ffffff; color: #344a65; padding: 5px 8px; box-shadow: none; }
#compact-panel button:hover { background-color: #eaf1fc; border-color: #9ab9e4; }
#compact-panel button.primary { background-color: #2465ce; border-color: #2465ce; color: #ffffff; font-weight: bold; }
#compact-panel button:disabled { opacity: 0.5; }
#compact-panel textview, #compact-panel textview text, #compact-panel list { background-color: #ffffff; color: #24334a; }
#compact-panel scrolledwindow { border: 1px solid #dfe5ee; border-radius: 5px; background-color: #ffffff; }
#compact-panel row { min-height: 28px; padding: 2px 8px; border-bottom: 1px solid #edf1f6; }
#compact-panel button.close-note { min-width: 24px; min-height: 24px; padding: 0; border: 0; background-color: transparent; font-size: 16pt; }
#compact-panel button.close-note:hover { background-color: #e4ebf5; }
#compact-panel .heading { font-size: 11pt; font-weight: bold; }
#compact-panel .hint { color: #71849d; font-size: 8pt; }
#compact-entry, #compact-entry-box { background-color: #2465ce; border-radius: 5px; }
#compact-entry button { min-width: 0; min-height: 0; padding: 0; margin: 0; border: 0; border-radius: 0; background-image: none; background-color: #2465ce; box-shadow: none; color: #ffffff; }
#compact-entry button:hover { background-color: #1a55b4; }
#compact-entry button:focus { outline: 1px solid #dceaff; outline-offset: -3px; }
#compact-entry label { color: #ffffff; font-weight: bold; font-size: 10pt; padding: 0; margin: 0; }
#compact-entry button label { font-size: 19pt; }
""")
Gtk.StyleContext.add_provider_for_screen(Gdk.Screen.get_default(), css, Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)

entry = Gtk.Window(type=Gtk.WindowType.TOPLEVEL)
entry.set_name("compact-entry")
entry.set_title("ProjNote 便签入口")
entry.set_wmclass("projnote-compact", "ProjNoteCompact")
entry.set_decorated(False)
# Fixed min/max hints below resize the real window, unlike a non-resizable
# natural-size requisition that can retain the old full label width.
entry.set_resizable(True)
entry.set_skip_taskbar_hint(True)
entry.set_skip_pager_hint(True)
entry.set_keep_above(True)
entry.set_accept_focus(False)
entry.set_type_hint(Gdk.WindowTypeHint.UTILITY)
entry.set_tooltip_text("打开便签")
entry_box = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=0)
entry_box.set_name("compact-entry-box")
entry_open = Gtk.Button(label="‹")
entry_open.set_can_focus(False)
entry_open.set_focus_on_click(False)
entry_open.set_tooltip_text("打开便签")
entry_label = entry_open.get_child()
entry_box.pack_start(entry_open, True, True, 0)
entry.add(entry_box)

panel = Gtk.Window(type=Gtk.WindowType.TOPLEVEL)
panel.set_title("ProjNote 便签")
panel.set_name("compact-panel")
panel.set_decorated(False)
panel.set_resizable(False)
panel.set_wmclass("projnote-native-note", "ProjNoteNativeNote")
panel.set_keep_above(True)
panel.set_skip_taskbar_hint(True)
panel.set_type_hint(Gdk.WindowTypeHint.UTILITY)
panel.set_default_size(340, 440)
panel.set_size_request(320, 420)
outer = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=7)
outer.set_border_width(10)
panel.add(outer)
heading = Gtk.Box(spacing=6)
heading_label = Gtk.Label(label="今日工作", xalign=0)
heading_label.get_style_context().add_class("heading")
heading.pack_start(heading_label, True, True, 0)
close_button = Gtk.Button(label="×")
close_button.get_style_context().add_class("close-note")
close_button.set_tooltip_text("关闭便签并返回箭头入口")
heading.pack_end(close_button, False, False, 0)
outer.pack_start(heading, False, False, 0)
mode_row = Gtk.Box(spacing=7)
mode = Gtk.ComboBoxText()
mode.append("task", "添加任务")
mode.append("record", "现场速记")
mode.set_active_id("task")
mode.set_tooltip_text("任务会加入今日任务；速记保存为现场记录")
mode_row.pack_start(mode, False, False, 0)
projects = Gtk.ComboBoxText()
projects.append("", "未归类")
projects.set_active_id("")
mode_row.pack_start(projects, True, True, 0)
outer.pack_start(mode_row, False, False, 0)
mode_hint = Gtk.Label(label="第一行作任务标题，其余内容保留为详情", xalign=0)
mode_hint.get_style_context().add_class("hint")
outer.pack_start(mode_hint, False, False, 0)
text = Gtk.TextView()
text.set_wrap_mode(Gtk.WrapMode.WORD_CHAR)
text.set_left_margin(6)
text.set_right_margin(6)
text.set_top_margin(6)
text.set_bottom_margin(6)
buffer = text.get_buffer()
scroll = Gtk.ScrolledWindow()
scroll.set_policy(Gtk.PolicyType.AUTOMATIC, Gtk.PolicyType.AUTOMATIC)
scroll.set_min_content_height(95)
scroll.add(text)
outer.pack_start(scroll, True, True, 0)
actions = Gtk.Box(spacing=6)
save_button = Gtk.Button(label="添加任务")
save_button.get_style_context().add_class("primary")
refresh_button = Gtk.Button(label="刷新任务")
restore_button = Gtk.Button(label="恢复草稿")
actions.pack_start(save_button, True, True, 0)
actions.pack_start(restore_button, False, False, 0)
actions.pack_start(refresh_button, False, False, 0)
outer.pack_start(actions, False, False, 0)
status = Gtk.Label(label="正在读取本机草稿…", xalign=0)
status.set_line_wrap(True)
status.set_max_width_chars(38)
status.get_style_context().add_class("hint")
outer.pack_start(status, False, False, 0)
outer.pack_start(Gtk.Separator(), False, False, 0)
outer.pack_start(Gtk.Label(label="今日任务（只读）", xalign=0), False, False, 0)
tasks = Gtk.ListBox()
tasks.set_selection_mode(Gtk.SelectionMode.NONE)
tasks_scroll = Gtk.ScrolledWindow()
tasks_scroll.set_min_content_height(132)
tasks_scroll.add(tasks)
outer.pack_start(tasks_scroll, True, True, 0)
footer = Gtk.Box(spacing=8)
full_button = Gtk.Button(label="打开完整工作台")
full_button.set_tooltip_text("在浏览器中打开工作台，便签正文继续保留")
footer.pack_start(full_button, False, False, 0)
version_label = Gtk.Label(label="版本读取中…", xalign=1)
version_label.get_style_context().add_class("hint")
footer.pack_end(version_label, True, True, 0)
outer.pack_start(footer, False, False, 0)


def current():
    return {"content": buffer.get_text(buffer.get_start_iter(), buffer.get_end_iter(), True),
            "project_id": projects.get_active_id() or "", "mode": mode.get_active_id() or "task"}


def set_editable(enabled):
    projects.set_sensitive(enabled)
    mode.set_sensitive(enabled)
    text.set_sensitive(enabled)
    save_button.set_sensitive(enabled and not state["loading"])
    restore_button.set_sensitive(enabled)


def update_mode(*_args):
    is_task = current()["mode"] == "task"
    save_button.set_label("添加任务" if is_task else "保存速记")
    mode_hint.set_text("第一行作任务标题，其余内容保留为详情" if is_task else "保存到现场记录，可稍后整理为日志")
    projects.set_tooltip_text("任务必须选择一个项目" if is_task else "速记可保存为未归类")
    schedule_draft()


def maybe_auto_project():
    if not state["auto_project"] or state["project_items"] is None:
        return
    state["auto_project"] = False
    value = current()
    if value["mode"] == "task" and not value["content"].strip() and not value["project_id"] and len(state["project_items"]) == 1:
        projects.set_active_id(state["project_items"][0]["id"])


def schedule_draft(*_args):
    if state["suppress"]:
        return
    state["dirty"] = True
    if state["debounce"]:
        GLib.source_remove(state["debounce"])
    state["debounce"] = GLib.timeout_add(1000, persist_draft)


def persist_draft():
    state["debounce"] = 0
    if state["loading"] or state["draft_busy"] or state["saving"] or state["conflict"] or not state["dirty"]:
        return False
    snapshot = current()
    state["draft_busy"] = True
    status.set_text("正在保存正文草稿…")

    def replied(message):
        state["draft_busy"] = False
        if message.get("error"):
            state["conflict"] = message.get("status") == 409
            status.set_text("草稿冲突，正文仍在本窗口；可保存为记录或明确恢复服务端草稿。" if state["conflict"] else "草稿未写入，输入保留：" + message["error"])
            return
        state["version"] = message["data"]["version"]
        state["dirty"] = current() != snapshot
        status.set_text("正文草稿已保存" if not state["dirty"] else "还有新输入待保存")
        if state["dirty"]:
            schedule_draft()

    request("draft", {**snapshot, "version": state["version"]}, replied)
    return False


def load_draft(explicit=False):
    snapshot_before = current()
    status.set_text("正在读取本机草稿…")

    def replied(message):
        state["loading"] = False
        save_button.set_sensitive(not state["saving"])
        if message.get("error"):
            status.set_text("无法读取草稿，输入保留：" + message["error"])
            return
        draft = message["data"]
        state["version"] = draft["version"]
        if explicit or (not state["dirty"] and current() == snapshot_before):
            state["suppress"] = True
            buffer.set_text(draft.get("content", ""))
            ensure_project(draft.get("project_id", ""))
            mode.set_active_id(draft.get("mode") or "record")
            update_mode()
            state["auto_project"] = state["first_load"] and not explicit and not draft.get("updated_at") and draft.get("mode") == "task" and not draft.get("content", "").strip() and not draft.get("project_id")
            state["suppress"] = False
            state["dirty"] = False
            state["conflict"] = False
            status.set_text("已恢复服务端草稿与模式" if explicit else "草稿就绪；收起窗口仍会保留")
            maybe_auto_project()
        else:
            state["conflict"] = True
            status.set_text("读取期间已有输入，已保留本地内容；可明确恢复服务端草稿。")
        state["first_load"] = False

    request("load_draft", callback=replied)


def ensure_project(project_id):
    if not projects.set_active_id(project_id):
        projects.append(project_id, "原草稿项目（需确认仍可写）")
        projects.set_active_id(project_id)


def refresh():
    def got_projects(message):
        if message.get("error"):
            status.set_text("项目读取失败，输入仍保留：" + message["error"])
            return
        selected = current()["project_id"]
        state["suppress"] = True
        projects.remove_all()
        projects.append("", "未归类")
        state["project_items"] = message["data"] or []
        for project in state["project_items"]:
            projects.append(project["id"], project["name"])
        ensure_project(selected)
        state["suppress"] = False
        maybe_auto_project()

    def got_today(message):
        if message.get("error"):
            status.set_text("今日任务读取失败，输入仍保留：" + message["error"])
            return
        for child in tasks.get_children():
            tasks.remove(child)
        items = message["data"] or []
        state["task_items"] = items
        for item in items:
            label = Gtk.Label(label=("进行中 · " if item.get("status") == "doing" else "待办 · ") + item.get("name", "") + ("  " + item["plan_end"] if item.get("plan_end") else ""), xalign=0)
            label.set_line_wrap(True)
            label.set_margin_top(0)
            label.set_margin_bottom(0)
            tasks.add(label)
        if not items:
            tasks.add(Gtk.Label(label="今日暂无任务", xalign=0))
        tasks.show_all()

    request("projects", callback=got_projects)
    request("today", callback=got_today)


def save(*_args):
    if state["saving"]:
        return
    if state["draft_busy"]:
        status.set_text("草稿正在提交，请稍后保存。")
        return
    submitted = current()
    is_task = submitted["mode"] == "task"
    if not submitted["content"].strip():
        status.set_text("请填写任务标题" if is_task else "请填写速记正文")
        return
    if is_task and not submitted["project_id"]:
        status.set_text("请选择项目后添加任务")
        return
    confirmed = "已添加到今日任务" if is_task else "速记已保存"
    state["saving"] = True
    set_editable(False)
    status.set_text("正在添加任务…" if is_task else "正在保存速记…")

    def saved(message):
        if message.get("error"):
            state["saving"] = False
            set_editable(True)
            status.set_text("保存失败，输入保留：" + message["error"])
            return
        state["suppress"] = True
        buffer.set_text("")
        state["suppress"] = False
        state["dirty"] = False
        # Clear only the exact server draft version known by this editor. A 409
        # preserves another editor's draft; never reload and retry that deletion.
        if state["version"] and not state["conflict"]:
            def cleared(reply):
                state["saving"] = False
                set_editable(True)
                if reply.get("error"):
                    state["conflict"] = True
                    status.set_text(confirmed + "，请勿重复提交；服务端草稿未清除，可明确恢复核对。")
                else:
                    state["version"] = reply["data"]["version"]
                    status.set_text(confirmed)
                refresh()
            request("draft", {"version": state["version"], "content": "", "project_id": submitted["project_id"], "mode": submitted["mode"]}, cleared)
        else:
            state["saving"] = False
            set_editable(True)
            status.set_text(confirmed + "；服务端其他草稿保留，请勿重复提交。")
            refresh()

    def flushed(message):
        if message.get("error"):
            state["saving"] = False
            set_editable(True)
            state["conflict"] = message.get("status") == 409
            status.set_text("正文仍保留，草稿写入未确认：" + message["error"])
            return
        state["version"] = message["data"]["version"]
        state["conflict"] = False
        request("save", submitted, saved)

    if not state["version"]:
        state["saving"] = False
        set_editable(True)
        status.set_text("请先恢复本机草稿连接，再保存；正文仍保留。")
        return
    if state["conflict"]:
        # A conflicting remote draft must never be overwritten. Save this local
        # content as a record without attempting to clear the other draft.
        request("save", submitted, saved)
    else:
        request("draft", {**submitted, "version": state["version"]}, flushed)


def restore(*_args):
    if state["saving"] or state["draft_busy"]:
        return
    if current()["content"].strip():
        dialog = Gtk.MessageDialog(transient_for=panel, modal=True, message_type=Gtk.MessageType.QUESTION,
                                   buttons=Gtk.ButtonsType.OK_CANCEL, text="用服务端草稿替换当前正文？")
        result = dialog.run()
        dialog.destroy()
        if result != Gtk.ResponseType.OK:
            return
    load_draft(True)


def settle_dock():
    state["settle_dock"] = 0
    area = state["dock_area"] or area_for(entry)
    entry.move(*dock_position(area, *entry.get_size()))
    return False


def dock(*_args):
    area = area_for(entry)
    state["dock_area"] = area
    width, height = min(26, area.width), min(52, area.height)
    geometry = Gdk.Geometry()
    geometry.min_width = geometry.max_width = width
    geometry.min_height = geometry.max_height = height
    entry.set_geometry_hints(None, geometry, Gdk.WindowHints.MIN_SIZE | Gdk.WindowHints.MAX_SIZE)
    entry.set_size_request(width, height)
    entry.resize(width, height)
    entry.move(*dock_position(area, width, height))
    if state["settle_dock"]:
        GLib.source_remove(state["settle_dock"])
    state["settle_dock"] = GLib.idle_add(settle_dock)
    if panel.get_visible():
        GLib.idle_add(position_panel)
    return False


def entry_configured(_widget, event):
    if state["dock_area"] is not None:
        target = dock_position(state["dock_area"], event.width, event.height)
        if (event.x, event.y) != target:
            entry.move(*target)
    return False


def show_entry(*_args):
    # A second launch/ShowEntry must not expose the icon beside an open editor.
    if not panel.get_visible():
        dock()
        entry.show_all()
    return True


def hide_panel(*_args):
    panel.hide()
    show_entry()
    persist_draft()
    return True


def panel_keypress(_widget, event):
    if event.keyval == Gdk.KEY_F4 and event.state & Gdk.ModifierType.MOD1_MASK:
        return hide_panel()
    return False


def position_panel():
    area = state["dock_area"] or area_for(entry)
    width, height = panel.get_size()
    x = area.x + max(0, area.width - width - 8)
    y = area.y + max(0, area.height - height - 8)
    if panel.get_position() != (x, y):
        panel.move(x, y)
    return False


def show_panel(*_args):
    dock()
    if not state["version"] and not state["loading"]:
        state["loading"] = True
        load_draft()
    panel.show_all()
    entry.hide()
    position_panel()
    GLib.idle_add(position_panel)
    panel.present()
    text.grab_focus()
    refresh()


menu = Gtk.Menu()
menu_actions = {}
for key, label, callback in [
    ("open_full", "打开完整工作台", lambda *_: request("open_full")),
    ("close", "退出便签入口（服务继续）", lambda *_: Gtk.main_quit()),
]:
    item = Gtk.MenuItem(label=label)
    item.connect("activate", callback)
    menu_actions[key] = item
    menu.append(item)
menu.show_all()


def context_menu(_widget, event):
    if event.button == 3:
        menu.popup_at_pointer(event)
        return True
    return False


def announce_ready(*_args):
    if not state["entry_ready"]:
        state["entry_ready"] = True
        print("READY GTK3-X11", flush=True)


# Test-only messages manipulate only widgets owned by this helper and are
# available solely to the owning process under explicit native opt-in.
def test_action(command):
    if os.environ.get("PROJNOTE_TEST_COMPACT_NATIVE") != "1":
        return
    action = command.get("test_action")
    if action == "open":
        show_panel()
    elif action == "fill":
        buffer.set_text(command.get("content", ""))
    elif action == "mode":
        mode.set_active_id(command.get("mode", "task"))
    elif action == "project":
        ensure_project(command.get("project_id", ""))
    elif action == "save":
        save_button.clicked()
    elif action == "full_button":
        full_button.clicked()
    elif action == "close_button":
        close_button.clicked()
    elif action == "entry_click":
        entry_open.clicked()
    elif action == "entry_pointer_click":
        # Deliver events only to this helper's owned native button. No global
        # desktop pointer or external input program is used by the test.
        window = entry_open.get_event_window() or entry_open.get_window()
        allocation = entry_open.get_allocation()
        device = Gdk.Display.get_default().get_default_seat().get_pointer()
        for kind in (Gdk.EventType.ENTER_NOTIFY, Gdk.EventType.BUTTON_PRESS, Gdk.EventType.BUTTON_RELEASE):
            event = Gdk.Event.new(kind)
            event.window = window
            event.send_event = True
            event.time = Gdk.CURRENT_TIME
            event.x, event.y = allocation.width / 2, allocation.height / 2
            event.set_device(device)
            if kind == Gdk.EventType.ENTER_NOTIFY:
                event.mode, event.detail = Gdk.CrossingMode.NORMAL, Gdk.NotifyType.NONLINEAR
                event.focus = False
            else:
                event.button = 1
                event.state = Gdk.ModifierType.BUTTON1_MASK if kind == Gdk.EventType.BUTTON_RELEASE else Gdk.ModifierType(0)
                ex, ey = entry.get_position()
                event.x_root, event.y_root = ex + event.x, ey + event.y
            entry_open.event(event)
    elif action == "alt_f4":
        event = Gdk.Event.new(Gdk.EventType.KEY_PRESS)
        event.window = panel.get_window()
        event.send_event = True
        event.keyval, event.state = Gdk.KEY_F4, Gdk.ModifierType.MOD1_MASK
        panel.event(event)
    elif action == "attempt_drag":
        # Events remain inside the helper's windows; there is no global input.
        widget = panel if command.get("target") == "panel" else entry
        for kind in (Gdk.EventType.BUTTON_PRESS, Gdk.EventType.MOTION_NOTIFY, Gdk.EventType.BUTTON_RELEASE):
            event = Gdk.Event.new(kind)
            event.window, event.send_event = widget.get_window(), True
            event.x, event.y, event.x_root, event.y_root = 10, 10, -500, -500
            event.set_device(Gdk.Display.get_default().get_default_seat().get_pointer())
            event.state = Gdk.ModifierType.BUTTON1_MASK
            if kind != Gdk.EventType.MOTION_NOTIFY:
                event.button = 1
            widget.event(event)
    elif action == "close_panel":
        panel.close()
    elif action == "menu":
        item = menu_actions.get(command.get("item"))
        if item:
            item.activate()
    elif action == "geometry_probe":
        geometry, work = Gdk.Rectangle(), Gdk.Rectangle()
        for name in ("x", "y", "width", "height"):
            setattr(geometry, name, int(command["geometry"][name]))
            setattr(work, name, int(command["workarea"][name]))
        area = usable_area(geometry, work)
        x, y = dock_position(area, int(command["width"]), int(command["height"]))
        print(json.dumps({"action": "test_state", "payload": {"x": x, "y": y, "area": {name: getattr(area, name) for name in ("x", "y", "width", "height")}}}), flush=True)
    elif action == "restore":
        load_draft(True)
    elif action == "inspect":
        ex, ey = entry.get_position()
        ew, eh = entry.get_size()
        px, py = panel.get_position()
        pw, ph = panel.get_size()
        panel_origin = panel.get_window().get_origin() if panel.get_window() else None
        panel_frame = panel.get_window().get_frame_extents() if panel.get_window() else None
        area = area_for(entry)
        snapshot = {**current(), "status": status.get_text(), "visible": panel.get_visible(),
                    "entry_visible": entry.get_visible(), "loading": state["loading"],
                    "saving": state["saving"], "draft_busy": state["draft_busy"],
                    "conflict": state["conflict"], "task_count": len(tasks.get_children()), "task_items": state["task_items"], "save_label": save_button.get_label(),
                    "entry_text": entry_open.get_label(), "panel_decorated": panel.get_decorated(), "panel_resizable": panel.get_resizable(),
                    "full_text": full_button.get_label(), "close_text": close_button.get_label(), "menu_items": list(menu_actions), "version_text": version_label.get_text(),
                    "task_view_height": tasks_scroll.get_allocated_height(), "task_row_heights": [child.get_allocated_height() for child in tasks.get_children()],
                    "entry_geometry": {"x": ex, "y": ey, "width": ew, "height": eh},
                    "panel_geometry": {name: getattr(panel_frame, name) for name in ("x", "y", "width", "height")} if panel_frame else {"x": px, "y": py, "width": pw, "height": ph},
                    "panel_xid": int(panel.get_window().get_xid()) if panel.get_window() else 0,
                    "panel_origin": list(panel_origin) if panel_origin else None,
                    "panel_frame": {name: getattr(panel_frame, name) for name in ("x", "y", "width", "height")} if panel_frame else None,
                    "workarea": {name: getattr(area, name) for name in ("x", "y", "width", "height")}}
        print(json.dumps({"action": "test_state", "payload": snapshot}, ensure_ascii=False), flush=True)


incoming = b""
def parent_input(_source, condition):
    global incoming
    if condition & (GLib.IO_HUP | GLib.IO_ERR):
        Gtk.main_quit()
        return False
    chunk = os.read(sys.stdin.fileno(), 65536)
    if not chunk:
        Gtk.main_quit()
        return False
    incoming += chunk
    while b"\n" in incoming:
        raw, incoming = incoming.split(b"\n", 1)
        try:
            message = json.loads(raw)
        except ValueError:
            continue
        if message.get("action") == "close":
            Gtk.main_quit()
            return False
        if message.get("action") == "show":
            show_panel()
            continue
        if message.get("action") == "show_entry":
            show_entry()
            continue
        if "test_action" in message:
            test_action(message)
            continue
        callback = state["pending"].pop(message.get("seq"), None)
        if callback:
            callback(message)
    return True


mode.connect("changed", update_mode)
buffer.connect("changed", schedule_draft)
projects.connect("changed", schedule_draft)
save_button.connect("clicked", save)
refresh_button.connect("clicked", lambda *_: refresh())
restore_button.connect("clicked", restore)
full_button.connect("clicked", lambda *_: request("open_full"))
close_button.connect("clicked", hide_panel)
panel.connect("realize", lambda *_: panel.get_window().set_functions(Gdk.WMFunction.CLOSE))
entry.connect("realize", lambda *_: entry.get_window().set_functions(Gdk.WMFunction(0)))
panel.connect("delete-event", hide_panel)
panel.connect("key-press-event", panel_keypress)
entry_open.add_events(Gdk.EventMask.BUTTON_PRESS_MASK | Gdk.EventMask.BUTTON_RELEASE_MASK)
entry_open.connect("button-press-event", context_menu)
entry_open.connect("clicked", show_panel)
entry.connect("configure-event", entry_configured)
panel.connect("size-allocate", lambda *_: GLib.idle_add(position_panel) if panel.get_visible() else None)
panel.connect("configure-event", lambda *_: position_panel() if panel.get_visible() else False)
entry.connect("destroy", lambda *_: Gtk.main_quit() if Gtk.main_level() else None)
entry.connect("map-event", announce_ready)
entry.get_screen().connect("monitors-changed", lambda *_: GLib.idle_add(dock))
entry.get_screen().connect("size-changed", lambda *_: GLib.idle_add(dock))
GLib.io_add_watch(sys.stdin.fileno(), GLib.IO_IN | GLib.IO_HUP | GLib.IO_ERR, parent_input)
entry.show_all()
GLib.idle_add(dock, True)
load_draft()
refresh()
request("version", callback=lambda reply: version_label.set_text(reply.get("data") or "版本未知"))
Gtk.main()
panel.destroy()
entry.destroy()
