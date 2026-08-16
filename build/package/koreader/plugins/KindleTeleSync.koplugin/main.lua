local InfoMessage = require("ui/widget/infomessage")
local NetworkMgr = require("ui/network/manager")
local QRWidget = require("ui/widget/qrwidget")
local TextViewer = require("ui/widget/textviewer")
local UIManager = require("ui/uimanager")
local WidgetContainer = require("ui/widget/container/widgetcontainer")
local _ = require("gettext")

local source = debug.getinfo(1, "S").source
local plugin_dir = source:match("^@(.+)/[^/]+$") or "."
local app_dir = "/mnt/us/extensions/KindleTeleSync"
local sync_binary = app_dir .. "/kindle_sync_d"
local updater_binary = app_dir .. "/updater"
local web_binary = app_dir .. "/webconfig"
local web_log = "/tmp/kindletelesync-web.log"

local KindleTeleSync = WidgetContainer:extend{
    name = "kindletelesync",
    is_doc_only = false,
}

local function shell_quote(value)
    return "'" .. tostring(value):gsub("'", "'\\''") .. "'"
end

local function file_exists(path)
    local f = io.open(path, "rb")
    if not f then return false end
    f:close()
    return true
end

function KindleTeleSync:show(text, warning)
    UIManager:show(InfoMessage:new{
        text = text,
        icon = warning and "notice-warning" or nil,
    })
end

function KindleTeleSync:run_binary(path, title)
    if not file_exists(path) then
        self:show(title .. " backend is missing. Reinstall KindleTeleSync.", true)
        return
    end
    self:show(title .. " started…")
    local pipe = io.popen(shell_quote(path) .. " 2>&1", "r")
    if not pipe then
        self:show("Cannot start " .. title .. ".", true)
        return
    end
    local output = pipe:read("*all") or ""
    local ok, _, code = pipe:close()
    if output == "" then output = ok and "Completed successfully." or "Operation failed without output." end
    if code and code ~= 0 then output = output .. "\n\nExit code: " .. tostring(code) end
    UIManager:show(TextViewer:new{ title = title, text = output })
end

function KindleTeleSync:sync_now()
    NetworkMgr:runWhenOnline(function()
        self:run_binary(sync_binary, "KindleTeleSync")
    end)
end

function KindleTeleSync:check_updates()
    NetworkMgr:runWhenOnline(function()
        self:run_binary(updater_binary, "KindleTeleSync update")
    end)
end

function KindleTeleSync:open_web_settings()
    NetworkMgr:runWhenOnline(function()
        if not file_exists(web_binary) then
            self:show("Web settings backend is missing. Reinstall KindleTeleSync.", true)
            return
        end

        local url_pipe = io.popen(shell_quote(web_binary) .. " --print-url 2>/dev/null", "r")
        if not url_pipe then
            self:show("Cannot determine the settings URL.", true)
            return
        end
        local url = (url_pipe:read("*l") or ""):gsub("%s+$", "")
        url_pipe:close()
        if url == "" then
            self:show("Cannot determine the settings URL.", true)
            return
        end

        os.execute("killall webconfig >/dev/null 2>&1")
        os.execute(shell_quote(web_binary) .. " >" .. shell_quote(web_log) .. " 2>&1 &")

        local qr = QRWidget:new{ text = url, width = 350, height = 350, scale_factor = 1 }
        local message
        message = InfoMessage:new{
            text = _("Scan the QR code or open this URL on a device connected to the same Wi-Fi network:\n\n") .. url .. _("\n\nThe settings server stops when this message is closed."),
            image = qr.image,
            alignment = "right",
            dismiss_callback = function()
                os.execute("killall webconfig >/dev/null 2>&1")
            end,
        }
        UIManager:show(message)
    end)
end

function KindleTeleSync:init()
    self.ui.menu:registerToMainMenu(self)
end

function KindleTeleSync:addToMainMenu(menu_items)
    menu_items.kindletelesync = {
        text = "KindleTeleSync",
        sorting_hint = "network",
        sub_item_table = {
            { text = "Sync now", callback = function() self:sync_now() end, separator = true },
            { text = "Open web settings", callback = function() self:open_web_settings() end },
            { text = "Check for updates", callback = function() self:check_updates() end },
        },
    }
end

return KindleTeleSync
