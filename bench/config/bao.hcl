// Bench-only OpenBao server config. Not used in
// production — the estate installs this plugin via IaC-managed
// tofu/ansible, never this file. Copied verbatim from the
// sibling repo's bench/config/bao.hcl — zero changes needed.
storage "file" {
  path = "/bao/data"
}

listener "tcp" {
  address     = "0.0.0.0:8200"
  tls_disable = true
}

plugin_directory = "/bao/plugins"

api_addr     = "http://127.0.0.1:8200"
cluster_addr = "http://127.0.0.1:8201"

ui = false
log_level = "info"
