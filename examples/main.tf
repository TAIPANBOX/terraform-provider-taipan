terraform {
  required_providers {
    taipan = {
      source  = "TAIPANBOX/taipan"
      version = "~> 0.1"
    }
  }
}

# cloud_url/cloud_key and wardryx_url/wardryx_key can also come from
# TOKENFUSE_CLOUD_URL/TOKENFUSE_CLOUD_KEY and WARDRYX_URL/WARDRYX_KEY, which
# keeps both keys out of version control. cloud_key must be an admin-role
# TokenFuse Cloud API key for taipan_budget to work; wardryx_key must be an
# admin-role Wardryx key (just the key segment of one WARDRYX_KEYS entry,
# not the full key:org:role triple) for taipan_wardryx_policy to work. All
# four are optional at this level -- only the pair the resources you
# actually use need are required.
provider "taipan" {
  cloud_url   = "https://cloud.tokenfuse.example"
  cloud_key   = var.tokenfuse_cloud_key
  wardryx_url = "https://wardryx.acme-bank.example"
  wardryx_key = var.wardryx_admin_key
}

variable "tokenfuse_cloud_key" {
  type      = string
  sensitive = true
}

variable "wardryx_admin_key" {
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
  attestation_detail = "spiffe://acme-bank.example/support/tier1-bot"

  labels = {
    env         = "prod"
    cost_center = "cs-eu"
  }

  output_path = "${path.module}/passports/tier1-bot.json"
}

output "support_bot_passport_json" {
  value = taipan_agent_passport.support_bot.json
}

# A Wardryx policy-as-code document, layered on top of Wardryx's own
# -policy/WARDRYX_POLICY file-loaded rules (which this resource never
# touches). Unlike taipan_budget, Wardryx has a real DELETE endpoint, so
# destroying this resource actually removes the rule from Wardryx.
resource "taipan_wardryx_policy" "ops_guard" {
  id     = "ops-guard"
  target = "agent://acme-bank.example/ops/*"

  deny_tool          = ["shell_exec"]
  max_steps          = 40
  deny_if_unattested = true
}
