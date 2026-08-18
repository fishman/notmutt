-- pickers.lua - the bundled picker library (R8): the script-facing
-- choosers. The core exposes only the picker_argv primitive (F4, argv
-- only); the tool wrappers live here so the core never hardcodes a
-- client. A script with another preference calls picker_argv itself,
-- and a user plugin may override these names before its action runs.
function picker_yazi()
    return picker_argv({ "yazi", "--chooser-file" })
end

function picker_ranger()
    return picker_argv({ "ranger", "--choosefile" })
end
