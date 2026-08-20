# Written from what an operator meets when the provider block is incomplete,
# and from the framework contract each resource's Configure has to satisfy.
#
# Same limit as the other feature file, stated again because it matters more
# here: these are bound to unit tests, and CLAUDE.md is clear that a green unit
# run "means very little in a Terraform provider". They hold what the operator
# is TOLD. They do not hold that anything applies.

Feature: Configuring the provider
  As an operator wiring the taipan provider into a Terraform configuration
  I want to be told exactly what is missing, before apply and in my own terms
  So that a missing setting is a sentence I can act on rather than a failure later

  @test:TestResourceConfigureHandlesEveryProviderDataItCanBeGiven
  Scenario: A resource is configured before, without, and with its backend
    Given Terraform is validating a configuration and has not configured the provider yet
    When a resource is configured with nothing
    Then it says nothing at all
    # This is the trap, not a formality. The framework calls Configure during
    # validation, before the provider block has been resolved. A resource that
    # raised a diagnostic there would fail every plan, including a plain
    # `terraform validate` with no credentials present, which is exactly when
    # somebody runs it for the first time.

    Given a provider that was configured but has no backend for this resource
    When the resource is configured
    Then it names the two environment variables that would supply it
    # A typo in those names sends an operator chasing a variable that does not
    # exist. Nothing had ever read them.

    Given a provider that was handed something that is not its client bundle
    When the resource is configured
    Then it reports a provider bug rather than failing later as a missing value

    Given a correctly configured provider
    When the resource is configured
    Then it says nothing

  @test:TestProviderMetadataNamesTheProviderTheWayUsersWriteIt
  Scenario: The name an operator types in a resource block
    Given the provider reports its own name and version
    Then the name is "taipan" and not "terraform-provider-taipan"
    And the version is the one the build passed in
    # Invariant 6 records the same confusion from the docs side: running
    # tfplugindocs without --provider-name rewrote all three page titles to
    # the repository name. This is that claim from the other end, and it is
    # what a `resource "taipan_budget"` block depends on.
