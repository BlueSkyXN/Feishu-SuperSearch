#!/usr/bin/env ruby
require "minitest/autorun"
require "fileutils"
require "rbconfig"
require "tmpdir"

require_relative "check-workflows"

class WorkflowShellcheckRunnerTest < Minitest::Test
  def ruby_command(source)
    [RbConfig.ruby, "-e", source, "--"]
  end

  def parse_fixture(workflow)
    Dir.mktmpdir("workflow-check") do |root|
      directory = File.join(root, ".github", "workflows")
      FileUtils.mkdir_p(directory)
      path = File.join(directory, "fixture.yml")
      File.write(path, workflow)
      parser = WorkflowChecks::WorkflowParser.new(root: root)
      yield parser.parse(path), parser.errors
    end
  end

  def test_starts_shellcheck_before_writing_a_large_script
    source = <<~'RUBY'
      input = STDIN.read
      abort "expected a large script" unless input.bytesize > 512
      STDOUT.write("[]\n")
    RUBY
    runner = WorkflowChecks::ShellcheckRunner.new(
      command: ruby_command(source),
      timeout_seconds: 1
    )

    diagnostics = runner.check("echo ok\n" * 128, shell: "bash", source: "fixture.yml:1")

    assert_empty diagnostics
  end

  def test_times_out_a_shellcheck_process_that_never_reads_stdin
    runner = WorkflowChecks::ShellcheckRunner.new(
      command: ruby_command("sleep 10"),
      timeout_seconds: 0.1
    )
    started = Process.clock_gettime(Process::CLOCK_MONOTONIC)

    error = assert_raises(WorkflowChecks::ShellcheckTimeout) do
      runner.check("echo blocked\n" * 100_000, shell: "bash", source: "fixture.yml:9")
    end

    elapsed = Process.clock_gettime(Process::CLOCK_MONOTONIC) - started
    assert_operator elapsed, :<, 2
    assert_match "fixture.yml:9", error.message
  end

  def test_missing_shellcheck_is_a_failure
    runner = WorkflowChecks::ShellcheckRunner.new(
      command: ["/definitely/missing/shellcheck"],
      timeout_seconds: 1
    )

    error = assert_raises(WorkflowChecks::ShellcheckExecutionError) do
      runner.check("echo ok\n", shell: "bash", source: "fixture.yml:2")
    end

    assert_match "cannot execute", error.message
  end

  def test_nonzero_shellcheck_without_diagnostics_is_a_failure
    runner = WorkflowChecks::ShellcheckRunner.new(
      command: ruby_command('STDIN.read; STDOUT.write("[]"); exit 7'),
      timeout_seconds: 1
    )

    error = assert_raises(WorkflowChecks::ShellcheckExecutionError) do
      runner.check("echo ok\n", shell: "bash", source: "fixture.yml:3")
    end

    assert_match "exited 7", error.message
  end

  def test_shellcheck_findings_are_returned_as_gate_errors
    diagnostic = [{ line: 2, column: 6, level: "warning", code: 2086,
                    message: "Double quote to prevent globbing" }].to_json
    runner = WorkflowChecks::ShellcheckRunner.new(
      command: ruby_command("STDIN.read; STDOUT.write(#{diagnostic.dump}); exit 1"),
      timeout_seconds: 1
    )

    findings = runner.check("echo $VALUE\n", shell: "bash", source: "fixture.yml:4")

    assert_equal 1, findings.length
    assert_match "fixture.yml:4: shellcheck SC2086:warning:1:6", findings.first
  end

  def test_invalid_shellcheck_json_is_a_failure
    runner = WorkflowChecks::ShellcheckRunner.new(
      command: ruby_command('STDIN.read; STDOUT.write("not-json")'),
      timeout_seconds: 1
    )

    error = assert_raises(WorkflowChecks::ShellcheckExecutionError) do
      runner.check("echo ok\n", shell: "bash", source: "fixture.yml:5")
    end

    assert_match "cannot parse ShellCheck JSON output", error.message
  end

  def test_expression_sanitizing_preserves_length
    script = "echo '${{ github.sha }}'\necho '${{ matrix.name }}'\n"

    sanitized = WorkflowChecks.sanitize_expressions(script)

    assert_equal script.length, sanitized.length
    refute_includes sanitized, "${{"
    assert_equal script.count("\n"), sanitized.count("\n")
  end

  def test_shell_normalization_is_limited_to_actionlint_supported_shells
    assert_equal "bash", WorkflowChecks.normalize_shell("bash")
    assert_equal "bash", WorkflowChecks.normalize_shell("bash --noprofile {0}")
    assert_equal "sh", WorkflowChecks.normalize_shell("sh")
    assert_equal "sh", WorkflowChecks.normalize_shell("sh -e {0}")
    assert_nil WorkflowChecks.normalize_shell("pwsh")
    assert_nil WorkflowChecks.normalize_shell("python")
  end

  def test_shell_selection_uses_step_and_job_before_workflow_defaults
    parse_fixture(<<~YAML) do |blocks, errors|
      name: Shell precedence
      on: push
      defaults:
        run:
          shell: sh
      jobs:
        linux:
          runs-on: ubuntu-latest
          defaults:
            run:
              shell: bash --noprofile {0}
          steps:
            - run: echo job
            - shell: sh -e {0}
              run: echo step
        windows:
          runs-on: windows-latest
          defaults: null
          steps:
            - run: Write-Host workflow-default-wins
    YAML
      assert_empty errors
      assert_equal ["bash --noprofile {0}", "sh -e {0}", "sh"], blocks.map(&:shell)
    end
  end

  def test_windows_runner_uses_powershell_without_a_shell_default
    parse_fixture(<<~YAML) do |blocks, errors|
      name: Windows shell
      on: push
      jobs:
        windows:
          runs-on: windows-latest
          steps:
            - run: Write-Host runner-default
    YAML
      assert_empty errors
      assert_equal ["pwsh"], blocks.map(&:shell)
    end
  end

  def test_malformed_steps_fail_closed
    parse_fixture(<<~YAML) do |blocks, errors|
      name: Broken steps
      on: push
      jobs:
        broken:
          runs-on: ubuntu-latest
          steps: not-a-sequence
    YAML
      assert_empty blocks
      assert_equal 1, errors.length
      assert_match "steps: must be a YAML sequence", errors.first
    end
  end

  def test_current_workflows_expose_every_run_block_to_shellcheck
    root = File.expand_path("..", __dir__)
    parser = WorkflowChecks::WorkflowParser.new(root: root)
    files = Dir[File.join(root, ".github", "workflows", "*.{yml,yaml}")].sort

    blocks = files.flat_map { |path| parser.parse(path) }

    assert_empty parser.errors
    assert_equal 29, blocks.length
    assert_equal 29, blocks.count { |block| WorkflowChecks.normalize_shell(block.shell) }
  end

  def test_makefile_disables_actionlint_shellcheck_integration
    makefile = File.read(File.expand_path("../Makefile", __dir__))

    assert_match(/actionlint.*-shellcheck ['"]{2}/, makefile)
  end
end
