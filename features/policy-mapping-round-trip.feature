# Written from what this provider promises a Registry user, and from the two
# invariants that govern the mapping layer.
#
# CLAUDE.md is blunt about the limit of anything in this file: "A green
# unit-test run means very little in a Terraform provider. Unit tests here
# check schema shape and mapping; they cannot see a perpetual diff, a missing
# RequiresReplace, or an import that does not round-trip. Only the acceptance
# tests can."
#
# So these scenarios claim exactly one thing: that the conversion between
# Terraform state and the wire is faithful in both directions. They do not
# claim any resource works. The acceptance suite claims that, and nothing here
# substitutes for it.
#
# Why the mapping is worth its own scenarios anyway: invariant 2 says "State
# reflects the backend, it never invents", and this is the layer where an
# invention would be introduced. It is also the layer where a mistake is
# silent. A policy that reaches Wardryx saying something other than what the
# operator wrote does not error; it just governs the wrong thing.

Feature: Turning a policy into the wire and back
  As an operator writing a Wardryx policy in Terraform
  I want what reaches Wardryx to be what I wrote, and what comes back to be what it stored
  So that a rule never quietly governs something other than what I declared

  @test:TestModelToDocumentCarriesEveryFieldTheOperatorWrote
  Scenario: Every field an operator set reaches the wire document
    Given a policy with a target, two denied tools, an allowed domain and every threshold set
    When it is converted for the API
    Then every one of those values appears in the document
    # A dropped field here is not an error, it is a weaker policy. A missing
    # deny_tool is a tool that is now allowed, and nothing says so.

  @test:TestModelToDocumentDoesNotInventListsTheOperatorLeftOut
  Scenario: Lists the operator did not write
    Given a policy whose deny_tool and allow_domains are null or not yet known
    When it is converted for the API
    Then the document carries no entries for them
    # Null and unknown are different states in Terraform and both mean "not
    # something the operator asked for". Reading either as a value is how
    # invariant 2's "never invents" gets broken at its cheapest point.

  @test:TestRecordToModelResolvesEveryOptionalToAConcreteValue
  Scenario: Wardryx returns a policy with its optional fields omitted
    Given a stored policy where every omitempty field was left off the wire
    When it is read back into Terraform state
    Then every attribute holds a concrete value rather than null
    # This is not tidiness. Each of those attributes is Computed with a
    # Default, so planning has already resolved an unset one to its zero
    # value. State holding null instead would disagree with the resolved plan,
    # and that disagreement is a perpetual diff: the exact bug CLAUDE.md says
    # unit tests cannot see. This test does not see it either. It holds the one
    # precondition that makes it impossible from THIS side.

  @test:TestStringListOrEmptyReturnsAnEmptyListNeverNull
  Scenario: A list attribute with nothing in it
    Given a policy field that came back with no entries
    When it becomes a Terraform list
    Then it is an empty list and not a null one

  @test:TestStringOrNullKeepsAnAbsentServerFieldOutOfState
  Scenario: The server did not send an optional string at all
    Given a passport field the API returned as an empty string
    When it becomes Terraform state
    Then state holds null rather than an empty string
    # The opposite rule to the one above, and deliberately so. This attribute
    # is Optional without a Default, so an empty string in state is a value
    # nobody set, and the next plan after an import shows a diff for a field
    # nobody changed.

  @test:TestResolveConfigValuePrefersConfigOverEnvironment
  Scenario: The same setting is given twice
    Given an environment variable and an explicit value in the Terraform config
    When the provider resolves the setting
    Then the config wins
    And an empty, null or not-yet-known config value falls back to the environment
    # Precedence nobody tested. Getting it backwards points a whole
    # configuration at the wrong backend, and it does it quietly, because both
    # values are legitimate.

  @test:TestAPIErrorNamesTheStatusAndTheServerMessage
  Scenario: The backend refuses a write
    Given the API answered with a status code and a message
    When the provider turns that into an error an operator reads
    Then it carries both
    # See the note in the test about what this does NOT hold: invariant 3's
    # error-text half is unchecked in this repository, and this error passes
    # the server's body through verbatim by design.
