terraform {
  required_version = ">= 1.6.0"
}

# The demo keeps its infrastructure as plain files: the pipeline only checks
# their shape, and nothing is applied.
locals {
  services = ["api", "web", "worker"]
}

output "service_names" {
  description = "The name of each service in this environment."
  value       = [for s in local.services : "${var.environment}-${s}"]
}
