terraform {
  required_providers {
    taipan = {
      source  = "TAIPANBOX/taipan"
      version = "~> 0.1"
    }
  }
}

# cloud_url and cloud_key can also come from TOKENFUSE_CLOUD_URL and
# TOKENFUSE_CLOUD_KEY, which keeps the key out of version control. cloud_key
# must be an admin-role TokenFuse Cloud API key for taipan_budget to work.
provider "taipan" {
  cloud_url = "https://cloud.tokenfuse.example"
  cloud_key = var.tokenfuse_cloud_key
}

variable "tokenfuse_cloud_key" {
  type      = string
  sensitive = true
}

# A central spend budget for one run. The TokenFuse gateway enforces this in
# real time: an over-budget call on this run gets a hard 402, not a warning
# after the fact.
resource "taipan_budget" "support_bot_daily" {
  run_id    = "support-bot-2026-07-09"
  limit_usd = 25.00
}

# An Agent Passport: identity, owner, runtime, and attestation posture for
# one agent. This resource calls no API; it renders and validates a static
# JSON document that Idryx/Qryx read from disk.
resource "taipan_agent_passport" "support_bot" {
  id                 = "agent://acme-bank.example/support/tier1-bot"
  owner              = "team-support@acme-bank.example"
  display_name       = "Tier-1 support bot"
  runtime            = "langgraph"
  attestation_method = "spiffe-svid"

  labels = {
    env         = "prod"
    cost_center = "cs-eu"
  }

  output_path = "${path.module}/passports/tier1-bot.json"
}

output "support_bot_passport_json" {
  value = taipan_agent_passport.support_bot.json
}
