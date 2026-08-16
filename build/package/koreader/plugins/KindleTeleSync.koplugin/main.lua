local ConfirmBox = require("ui/widget/confirmbox")
local InfoMessage = require("ui/widget/infomessage")
local InputDialog = require("ui/widget/inputdialog")
local JSON = require("json")
local NetworkMgr = require("ui/network/manager")
local QRWidget = require("ui/widget/qrwidget")
local TextViewer = require("ui/widget/textviewer")
local UIManager = require("ui/uimanager")
local WidgetContainer = require("ui/widget/container/widgetcontainer")
local _ = require("gettext")

local app_dir = "/mnt/us/extensions/KindleTeleSync"
local binary_path = app_dir .. "/kindletelesync"
local config_path = app_dir .. "/config.json"
local log_path = app_dir .. "/sync.log"

local KindleTeleSync = WidgetContainer:extend{
    name = "kindletelesync",
    is_doc_only = false,
}

local function shell_quote(value)
    return "'" .. tostring(value):gsub("'", "'\\''") .. "'"
end

local function read_all(path)
    local f = io.open(path, "rb")
    if not f then return nil end
    local data = f:read("*all")
    f:close()
    return data
end

local function file_exists(path)
    local f = io.open(path, "rb")
    if not f then return false end
    f:close()
    return true
end

local function ensure_defaults(cfg)
    cfg.proxy = cfg.proxy or {}
    cfg.proxy.type = cfg.proxy.type or "socks5"
    if cfg.proxy.enabled == nil then cfg.proxy.enabled = false end
    cfg.updates_state = cfg.updates_state or { pts = 0, date = 0, qts = 0 }
    cfg.allowed_extensions = cfg.allowed_extensions or { ".epub", ".mobi", ".pdf", ".zip", ".fb2" }
    cfg.download_path = cfg.download_path or "/mnt/us/books"
    cfg.max_file_bytes = cfg.max_file_bytes or 104857600
    cfg.max_files_per_sync = cfg.max_files_per_sync or 20
    cfg.max_total_bytes = cfg.max_total_bytes or 262144000
    cfg.sync_timeout_seconds = cfg.sync_timeout_seconds or 600
    cfg.web_timeout_seconds = cfg.web_timeout_seconds or 180
    if cfg.send_notifications == nil then cfg.send_notifications = true end
    return cfg
end

local function load_config()
    local raw = read_all(config_path)
    if not raw then return nil, "Cannot read " .. config_path end
    if raw:sub(1, 3) == "\239\187\191" then raw = raw:sub(4) end
    local ok, cfg = pcall(JSON.decode, raw)
    if not ok or type(cfg) ~= "table" then return nil, "Configuration file is not valid JSON." end
    return ensure_defaults(cfg)
end

local function save_config(cfg)
    local ok, encoded = pcall(JSON.encode, cfg)
    if not ok then return false, "Cannot encode configuration." end
    local tmp = config_path .. ".tmp"
    local f, err = io.open(tmp, "wb")
    if not f then return false, tostring(err) end
    local wrote, werr = f:write(encoded .. "\n")
    f:close()
    if not wrote then os.remove(tmp); return false, tostring(werr) end
    os.execute("chmod 600 " .. shell_quote(tmp))
    local renamed, rerr = os.rename(tmp, config_path)
    if not renamed then os.remove(tmp); return false, tostring(rerr) end
    return true
end

local function basename(path)
    return tostring(path):match("([^/]+)$") or tostring(path)
end

function KindleTeleSync:show(text, warning)
    UIManager:show(InfoMessage:new{
        text = text,
        icon = warning and "notice-warning" or nil,
    })
end

function KindleTeleSync:show_result(result, title)
    if not result then return end
    local lines = { result.message or (result.ok and "Done." or "Operation failed.") }
    local downloaded = result.downloaded or {}
    if #downloaded > 0 then
        table.insert(lines, "")
        table.insert(lines, "Downloaded:")
        for i = 1, math.min(#downloaded, 12) do
            table.insert(lines, "• " .. basename(downloaded[i]))
        end
        if #downloaded > 12 then table.insert(lines, string.format("…and %d more.", #downloaded - 12)) end
    end
    local skipped = result.skipped or {}
    if #skipped > 0 then
        table.insert(lines, "")
        table.insert(lines, "Warnings / skipped:")
        for i = 1, math.min(#skipped, 10) do table.insert(lines, "• " .. tostring(skipped[i])) end
        if #skipped > 10 then table.insert(lines, string.format("…and %d more.", #skipped - 10)) end
    end
    local errors = result.errors or {}
    if #errors > 0 then
        table.insert(lines, "")
        table.insert(lines, "Errors:")
        for i = 1, math.min(#errors, 8) do table.insert(lines, "• " .. tostring(errors[i])) end
        if #errors > 8 then table.insert(lines, string.format("…and %d more.", #errors - 8)) end
    end
    local details = result.details or {}
    local detail_keys = { "version", "goarm", "local_ip", "free_space", "download_path", "asset" }
    local shown = false
    for _, key in ipairs(detail_keys) do
        if details[key] ~= nil then
            if not shown then table.insert(lines, ""); table.insert(lines, "Details:"); shown = true end
            table.insert(lines, key .. ": " .. tostring(details[key]))
        end
    end
    UIManager:show(TextViewer:new{ title = title or "KindleTeleSync", text = table.concat(lines, "\n") })
end

function KindleTeleSync:run_backend(command, title)
    if not file_exists(binary_path) then
        self:show("KindleTeleSync backend is missing. Reinstall the package.", true)
        return nil
    end
    self:show(title .. "…")
    local pipe = io.popen(shell_quote(binary_path) .. " " .. command .. " --json", "r")
    if not pipe then self:show("Cannot start KindleTeleSync.", true); return nil end
    local output = pipe:read("*all") or ""
    pipe:close()
    local ok, result = pcall(JSON.decode, output)
    if not ok or type(result) ~= "table" then
        self:show("KindleTeleSync returned an invalid response.\n\n" .. output, true)
        return nil
    end
    self:show_result(result, title)
    return result
end

function KindleTeleSync:run_online(command, title)
    NetworkMgr:runWhenOnline(function() self:run_backend(command, title) end)
end

function KindleTeleSync:edit_top(key, title, input_type, to_display, from_input, reset_state)
    local cfg, err = load_config()
    if not cfg then self:show(err, true); return end
    local current = cfg[key]
    if to_display then current = to_display(current) end
    local dialog
    dialog = InputDialog:new{
        title = title,
        input = tostring(current or ""),
        input_type = input_type or "text",
        buttons = {{
            { text = "Cancel", id = "close", callback = function() UIManager:close(dialog) end },
            { text = "Save", is_enter_default = true, callback = function()
                local value = dialog:getInputText()
                if from_input then
                    local converted, convert_err = from_input(value)
                    if converted == nil then self:show(convert_err or "Invalid value.", true); return end
                    value = converted
                elseif input_type == "number" then
                    value = tonumber(value)
                    if not value then self:show("Please enter a valid number.", true); return end
                end
                if reset_state and cfg[key] ~= value then cfg.updates_state = { pts = 0, date = 0, qts = 0 } end
                cfg[key] = value
                local saved, save_err = save_config(cfg)
                if not saved then self:show("Cannot save configuration: " .. tostring(save_err), true); return end
                UIManager:close(dialog)
            end },
        }},
    }
    UIManager:show(dialog)
    dialog:onShowKeyboard()
end

function KindleTeleSync:edit_proxy(key, title, input_type)
    local cfg, err = load_config()
    if not cfg then self:show(err, true); return end
    local dialog
    dialog = InputDialog:new{
        title = title,
        input = tostring(cfg.proxy[key] or ""),
        input_type = input_type or "text",
        buttons = {{
            { text = "Cancel", id = "close", callback = function() UIManager:close(dialog) end },
            { text = "Save", is_enter_default = true, callback = function()
                cfg.proxy[key] = dialog:getInputText()
                local saved, save_err = save_config(cfg)
                if not saved then self:show("Cannot save configuration: " .. tostring(save_err), true); return end
                UIManager:close(dialog)
            end },
        }},
    }
    UIManager:show(dialog)
    dialog:onShowKeyboard()
end

function KindleTeleSync:edit_extensions()
    self:edit_top("allowed_extensions", "Allowed extensions", "text",
        function(value) return table.concat(value or {}, ", ") end,
        function(value)
            local out = {}
            for ext in tostring(value):gmatch("[^,]+") do
                ext = ext:gsub("^%s+", ""):gsub("%s+$", "")
                if ext ~= "" then table.insert(out, ext) end
            end
            if #out == 0 then return nil, "Enter at least one extension." end
            return out
        end)
end

function KindleTeleSync:edit_megabytes(key, title)
    self:edit_top(key, title, "number",
        function(value) return math.floor((tonumber(value) or 0) / 1048576) end,
        function(value)
            local n = tonumber(value)
            if not n or n <= 0 then return nil, "Enter a positive size in MiB." end
            return math.floor(n * 1048576)
        end)
end

function KindleTeleSync:toggle_top(key)
    local cfg, err = load_config()
    if not cfg then self:show(err, true); return end
    cfg[key] = not cfg[key]
    local ok, save_err = save_config(cfg)
    if not ok then self:show("Cannot save configuration: " .. tostring(save_err), true) end
end

function KindleTeleSync:toggle_proxy()
    local cfg, err = load_config()
    if not cfg then self:show(err, true); return end
    cfg.proxy.enabled = not cfg.proxy.enabled
    local ok, save_err = save_config(cfg)
    if not ok then self:show("Cannot save configuration: " .. tostring(save_err), true) end
end

function KindleTeleSync:is_top_enabled(key)
    local cfg = load_config()
    return cfg and cfg[key] == true or false
end

function KindleTeleSync:is_proxy_enabled()
    local cfg = load_config()
    return cfg and cfg.proxy.enabled == true or false
end

function KindleTeleSync:proxy_type_is(value)
    local cfg = load_config()
    return cfg and cfg.proxy.type == value or false
end

function KindleTeleSync:set_proxy_type(value)
    local cfg, err = load_config()
    if not cfg then self:show(err, true); return end
    cfg.proxy.type = value
    local ok, save_err = save_config(cfg)
    if not ok then self:show("Cannot save configuration: " .. tostring(save_err), true) end
end

function KindleTeleSync:reset_state()
    UIManager:show(ConfirmBox:new{
        text = "Reset Telegram synchronization state?\n\nThe next sync will initialize from the current Telegram state and will not download old history.",
        ok_text = "Reset",
        ok_callback = function()
            local cfg, err = load_config()
            if not cfg then self:show(err, true); return end
            cfg.updates_state = { pts = 0, date = 0, qts = 0 }
            local ok, save_err = save_config(cfg)
            if not ok then self:show("Cannot save configuration: " .. tostring(save_err), true); return end
            self:show("Telegram synchronization state was reset.")
        end,
    })
end

function KindleTeleSync:open_web_settings()
    NetworkMgr:runWhenOnline(function()
        if not file_exists(binary_path) then self:show("KindleTeleSync backend is missing.", true); return end
        os.execute(shell_quote(binary_path) .. " web-stop >/dev/null 2>&1")
        local pipe = io.popen(shell_quote(binary_path) .. " web-url 2>/dev/null", "r")
        if not pipe then self:show("Cannot determine settings URL.", true); return end
        local url = (pipe:read("*l") or ""):gsub("%s+$", "")
        pipe:close()
        if url == "" then self:show("Cannot determine settings URL.", true); return end
        os.execute(shell_quote(binary_path) .. " web >/dev/null 2>&1 &")
        local qr = QRWidget:new{ text = url, width = 350, height = 350, scale_factor = 1 }
        UIManager:show(InfoMessage:new{
            text = _("Scan the QR code or open this URL on a device connected to the same Wi-Fi network:\n\n") .. url .. _("\n\nThe server also stops automatically after the configured timeout."),
            image = qr.image,
            alignment = "right",
            dismiss_callback = function()
                os.execute(shell_quote(binary_path) .. " web-stop >/dev/null 2>&1")
            end,
        })
    end)
end

function KindleTeleSync:show_log()
    local raw = read_all(log_path)
    if not raw or raw == "" then self:show("No KindleTeleSync log is available yet."); return end
    if #raw > 24000 then raw = "… showing the last 24 KB …\n\n" .. raw:sub(#raw - 24000) end
    UIManager:show(TextViewer:new{ title = "KindleTeleSync log", text = raw })
end

function KindleTeleSync:init()
    self.ui.menu:registerToMainMenu(self)
end

function KindleTeleSync:addToMainMenu(menu_items)
    menu_items.kindletelesync = {
        text = "KindleTeleSync",
        sorting_hint = "network",
        sub_item_table = {
            { text = "Sync now", callback = function() self:run_online("sync", "KindleTeleSync sync") end, separator = true },
            { text = "Test Telegram", callback = function() self:run_online("test", "Telegram test") end },
            { text = "Run diagnostics", callback = function() self:run_backend("diagnostics", "KindleTeleSync diagnostics") end },
            { text = "Settings", sub_item_table = {
                { text = "Bot token", keep_menu_open = true, callback = function() self:edit_top("bot_token", "Bot token", "password", nil, nil, true) end },
                { text = "Chat ID", keep_menu_open = true, callback = function() self:edit_top("chat_id", "Chat ID", "number", nil, nil, true) end },
                { text = "Allowed extensions", keep_menu_open = true, callback = function() self:edit_extensions() end },
                { text = "Download directory", keep_menu_open = true, callback = function() self:edit_top("download_path", "Download directory") end },
                { text = "Send Telegram summaries", checked_func = function() return self:is_top_enabled("send_notifications") end, callback = function() self:toggle_top("send_notifications") end, separator = true },
                { text = "Safety limits", sub_item_table = {
                    { text = "Maximum file size (MiB)", keep_menu_open = true, callback = function() self:edit_megabytes("max_file_bytes", "Maximum file size (MiB)") end },
                    { text = "Maximum files per sync", keep_menu_open = true, callback = function() self:edit_top("max_files_per_sync", "Maximum files per sync", "number") end },
                    { text = "Maximum total per sync (MiB)", keep_menu_open = true, callback = function() self:edit_megabytes("max_total_bytes", "Maximum total download (MiB)") end },
                    { text = "Sync timeout (seconds)", keep_menu_open = true, callback = function() self:edit_top("sync_timeout_seconds", "Sync timeout (seconds)", "number") end },
                    { text = "Web settings timeout (seconds)", keep_menu_open = true, callback = function() self:edit_top("web_timeout_seconds", "Web settings timeout (seconds)", "number") end },
                }},
                { text = "Proxy", sub_item_table = {
                    { text = "Enable proxy", checked_func = function() return self:is_proxy_enabled() end, callback = function() self:toggle_proxy() end },
                    { text = "SOCKS5", checked_func = function() return self:proxy_type_is("socks5") end, callback = function() self:set_proxy_type("socks5") end },
                    { text = "HTTP CONNECT", checked_func = function() return self:proxy_type_is("http") end, callback = function() self:set_proxy_type("http") end },
                    { text = "MTProto", checked_func = function() return self:proxy_type_is("mtproto") end, callback = function() self:set_proxy_type("mtproto") end, separator = true },
                    { text = "Proxy address", keep_menu_open = true, callback = function() self:edit_proxy("address", "Proxy address (host:port)") end },
                    { text = "Proxy username", keep_menu_open = true, callback = function() self:edit_proxy("username", "Proxy username") end },
                    { text = "Proxy password", keep_menu_open = true, callback = function() self:edit_proxy("password", "Proxy password", "password") end },
                    { text = "MTProto secret", keep_menu_open = true, callback = function() self:edit_proxy("mtproto_secret", "MTProto secret", "password") end },
                }},
                { text = "Reset Telegram sync state", callback = function() self:reset_state() end, separator = true },
                { text = "Open web settings", callback = function() self:open_web_settings() end },
            }, separator = true },
            { text = "Update KindleTeleSync", callback = function() self:run_online("update", "KindleTeleSync update") end },
            { text = "Show version", callback = function() self:run_backend("version", "KindleTeleSync version") end },
            { text = "Show last log", callback = function() self:show_log() end },
        },
    }
end

return KindleTeleSync
