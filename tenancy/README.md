# Multi-Tenant / Multi-Project Governance

This layer prevents one project, customer, or repository from leaking state into
another when MARSHAL runs as a shared service.

Single-project local mode may use one implicit tenant, but the runtime schema
should still preserve a namespace boundary.
