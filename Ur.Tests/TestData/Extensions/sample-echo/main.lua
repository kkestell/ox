ur.tool.register({
  name = "__TOOL_NAME__",
  description = "Echoes text back to the caller.",
  parameters = {
    type = "object",
    properties = {
      text = { type = "string" }
    },
    required = { "text" }
  },
  handler = function(args)
    return args.text
  end
})
