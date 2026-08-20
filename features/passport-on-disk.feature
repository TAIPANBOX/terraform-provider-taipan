# What `taipan_agent_passport` does to a FILESYSTEM, which is the one thing
# about this resource that no plan shows and no state records.
#
# Unlike the other two feature files, these scenarios are bound to ACCEPTANCE
# tests, so under invariant 9 they may claim the resource works and not only
# that the mapping is faithful. That is the distinction CLAUDE.md draws and it
# is worth stating where it applies rather than leaving it to be inferred: the
# gate binding scenarios to tests cannot tell a unit test from an acceptance
# one, and only these two may make this kind of claim.
#
# Both scenarios exist because the failure they describe is a QUIET one. There
# is no error, no diff, and no line in the plan. The operator's belief about
# what is on disk simply stops matching what is on disk.

Feature: An agent passport as a file on the operator's disk

  @test:TestAccAgentPassportResource_MovingTheOutputPathLeavesNoStaleCopyBehind
  Scenario: Moving a passport does not leave the old one behind
    Given a passport written to one path
    When the operator changes output_path and applies again
    Then the passport is at the new path
    And nothing is left at the old one
    # A leftover is not a broken file, which is why this needs a test. It
    # parses, it validates, and whatever reads it gets an attestation that
    # used to be true. Two passports now exist for one agent and neither
    # announces itself as the stale one.

  @test:TestAccAgentPassportResource_MissingParentDirectoryFailsLoudlyRatherThanCreatingIt
  Scenario: A directory the operator did not create is not created for them
    Given an output_path whose parent directory does not exist
    When the operator applies
    Then the apply fails and says it could not write output_path
    And the directory is still not there
    # An apply that silently makes directories is taking a side effect nobody
    # wrote down. The refusal has to be loud, because a silent mkdir and a
    # silent skip look identical from the plan, and both end with an operator
    # believing a passport is somewhere it is not.
