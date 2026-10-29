resource "mistral_agent" "support" {
  name         = "support"
  model        = "mistral-medium-latest"
  description  = "Answers questions on orders."
  instructions = "Answer from the order data only, and say so when it does not cover the question."

  tools = [
    { type = "web_search" },
    {
      type = "function"
      function = {
        name        = "get_order"
        description = "Returns an order by its ID."
        parameters = jsonencode({
          type       = "object"
          properties = { order_id = { type = "string" } }
          required   = ["order_id"]
        })
      }
    },
  ]

  completion_args = {
    temperature = 0.2
  }

  metadata = { team = "support" }
}
