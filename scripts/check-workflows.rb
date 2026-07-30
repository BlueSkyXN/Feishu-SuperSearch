#!/usr/bin/env ruby
# Ruby-stdlib GitHub Actions structure and shell validation.
require "json"
require "open3"
require "psych"

module WorkflowChecks
  SHELLCHECK_EXCLUDES = "SC1091,SC2194,SC2050,SC2154,SC2157,SC2043".freeze
  DEFAULT_SHELLCHECK_TIMEOUT = 10.0

  class Error < StandardError; end
  class ShellcheckTimeout < Error; end
  class ShellcheckExecutionError < Error; end

  RunBlock = Struct.new(:script, :shell, :source)
  CommandResult = Struct.new(:stdout, :stderr, :status, :timed_out)
  CheckResult = Struct.new(:files, :run_blocks, :checked_blocks, :skipped_blocks, :errors)

  module_function

  def normalize_shell(shell)
    return "bash" if shell == "bash" || shell&.start_with?("bash ")
    return "sh" if shell == "sh" || shell&.start_with?("sh ")

    nil
  end

  # Keep actionlint's substitution behavior: expressions become inert tokens while
  # preserving their length for useful ShellCheck line and column diagnostics.
  def sanitize_expressions(script)
    script.gsub(/\$\{\{.*?\}\}/m) { |expression| "_" * expression.length }
  end

  class CommandRunner
    TERMINATION_GRACE_SECONDS = 1.0

    def capture(argv, stdin_data:, timeout_seconds:)
      stdout_text = ""
      stderr_text = ""
      status = nil
      timed_out = false
      spawn_options = Gem.win_platform? ? {} : { pgroup: true }

      Open3.popen3(*argv, spawn_options) do |stdin, stdout, stderr, wait_thread|
        writer_error = nil
        writer = Thread.new do
          Thread.current.report_on_exception = false if Thread.current.respond_to?(:report_on_exception=)
          begin
            stdin.write(stdin_data)
          rescue Errno::EPIPE, IOError
            # The child may exit before consuming all input. Its status and stderr
            # below provide the authoritative failure.
          rescue StandardError => e
            writer_error = e
          ensure
            stdin.close unless stdin.closed?
          end
        end
        stdout_reader = Thread.new { stdout.read }
        stderr_reader = Thread.new { stderr.read }

        unless wait_thread.join(timeout_seconds)
          timed_out = true
          terminate(wait_thread.pid, "TERM")
          unless wait_thread.join(TERMINATION_GRACE_SECONDS)
            terminate(wait_thread.pid, "KILL")
            wait_thread.join
          end
        end

        writer.join
        stdout_text = stdout_reader.value
        stderr_text = stderr_reader.value
        status = wait_thread.value
        raise writer_error if writer_error
      end

      CommandResult.new(stdout_text, stderr_text, status, timed_out)
    rescue Errno::ENOENT => e
      raise ShellcheckExecutionError, "cannot execute #{argv.first.inspect}: #{e.message}"
    end

    private

    def terminate(pid, signal)
      target = Gem.win_platform? ? pid : -pid
      Process.kill(signal, target)
    rescue Errno::ESRCH, Errno::EPERM
      nil
    end
  end

  class ShellcheckRunner
    def initialize(command:, timeout_seconds:, command_runner: CommandRunner.new)
      @command = Array(command)
      @timeout_seconds = Float(timeout_seconds)
      @command_runner = command_runner
      raise ArgumentError, "shellcheck command must not be empty" if @command.empty? || @command.first.to_s.empty?
      raise ArgumentError, "shellcheck timeout must be greater than zero" unless @timeout_seconds.positive?
    end

    def check(script, shell:, source:)
      normalized_shell = WorkflowChecks.normalize_shell(shell)
      raise ArgumentError, "unsupported shell #{shell.inspect}" unless normalized_shell

      prepared_script = if normalized_shell == "bash"
                          "set -eo pipefail\n#{WorkflowChecks.sanitize_expressions(script)}\n"
                        else
                          "set -e\n#{WorkflowChecks.sanitize_expressions(script)}\n"
                        end
      argv = @command + [
        "--norc", "-f", "json", "-x", "--shell", normalized_shell,
        "-e", SHELLCHECK_EXCLUDES, "-"
      ]
      result = @command_runner.capture(
        argv,
        stdin_data: prepared_script,
        timeout_seconds: @timeout_seconds
      )

      if result.timed_out
        raise ShellcheckTimeout,
              "#{source}: ShellCheck exceeded #{@timeout_seconds}s and was terminated"
      end

      diagnostics = parse_diagnostics(result.stdout, source)
      unless result.status.success?
        if diagnostics.empty?
          detail = result.stderr.to_s.strip
          detail = "no diagnostic output" if detail.empty?
          raise ShellcheckExecutionError,
                "#{source}: ShellCheck exited #{result.status.exitstatus}: #{detail}"
        end
      end

      diagnostics
    end

    private

    def parse_diagnostics(output, source)
      parsed = JSON.parse(output)
      unless parsed.is_a?(Array)
        raise ShellcheckExecutionError, "#{source}: ShellCheck JSON output must be an array"
      end

      parsed.map do |diagnostic|
        unless diagnostic.is_a?(Hash)
          raise ShellcheckExecutionError, "#{source}: ShellCheck returned a malformed diagnostic"
        end

        line = [diagnostic.fetch("line", 1).to_i - 1, 1].max
        column = diagnostic.fetch("column", 1).to_i
        code = diagnostic.fetch("code", "unknown")
        level = diagnostic.fetch("level", "error")
        message = diagnostic.fetch("message", "ShellCheck issue")
        "#{source}: shellcheck SC#{code}:#{level}:#{line}:#{column}: #{message}"
      end
    rescue JSON::ParserError => e
      raise ShellcheckExecutionError,
            "#{source}: cannot parse ShellCheck JSON output: #{e.message}"
    end
  end

  class WorkflowParser
    attr_reader :errors

    def initialize(root:)
      @root = File.expand_path(root)
      @errors = []
    end

    def parse(path)
      document = Psych.parse_file(path)
      root_node = document&.root
      relative_path = path.sub(@root + File::SEPARATOR, "")
      unless root_node.is_a?(Psych::Nodes::Mapping)
        @errors << "#{relative_path}: workflow root must be a YAML mapping"
        return []
      end

      root_keys = root_node.children.each_slice(2).map do |key_node, _value_node|
        key_node.value if key_node.is_a?(Psych::Nodes::Scalar)
      end.compact
      %w[name on jobs].each do |key|
        @errors << "#{relative_path}: missing top-level #{key}:" unless root_keys.include?(key)
      end

      workflow = document.to_ruby
      jobs = workflow["jobs"]
      unless jobs.is_a?(Hash)
        @errors << "#{relative_path}: top-level jobs: must be a YAML mapping"
        return []
      end

      parse_jobs(jobs, relative_path, workflow.dig("defaults", "run", "shell"))
    rescue Psych::SyntaxError => e
      relative_path ||= path.sub(@root + File::SEPARATOR, "")
      @errors << "#{relative_path}: #{e.message}"
      []
    end

    private

    def parse_jobs(jobs, relative_path, workflow_shell)
      blocks = []
      jobs.each do |job_name, job|
        unless job.is_a?(Hash)
          @errors << "#{relative_path}: job #{job_name.inspect} must be a YAML mapping"
          next
        end

        steps = job["steps"]
        if steps.nil? && job.key?("uses")
          next
        end
        unless steps.is_a?(Array)
          @errors << "#{relative_path}: job #{job_name.inspect} steps: must be a YAML sequence"
          next
        end

        job_shell = job.dig("defaults", "run", "shell")
        runner_shell = windows_runner?(job["runs-on"]) ? "pwsh" : "bash"
        steps.each_with_index do |step, index|
          unless step.is_a?(Hash)
            @errors << "#{relative_path}: job #{job_name.inspect} step #{index + 1} must be a YAML mapping"
            next
          end

          next unless step.key?("run")
          unless step["run"].is_a?(String)
            @errors << "#{relative_path}: job #{job_name.inspect} step #{index + 1} run: must be a YAML scalar"
            next
          end

          shell = step["shell"] || job_shell || workflow_shell || runner_shell
          blocks << RunBlock.new(
            step["run"],
            shell,
            "#{relative_path}: job #{job_name.inspect} step #{index + 1}"
          )
        end
      end
      blocks
    end

    def windows_runner?(runs_on)
      labels = runs_on.is_a?(Array) ? runs_on : [runs_on].compact
      labels.any? do |label|
        normalized = label.to_s.downcase
        normalized == "windows" || normalized.start_with?("windows-")
      end
    end
  end

  class Checker
    def initialize(root:, shellcheck_runner:)
      @root = File.expand_path(root)
      @shellcheck_runner = shellcheck_runner
    end

    def run
      files = Dir[File.join(@root, ".github", "workflows", "*.{yml,yaml}")].sort
      raise Error, "no workflow files found" if files.empty?

      parser = WorkflowParser.new(root: @root)
      run_blocks = files.flat_map { |path| parser.parse(path) }
      errors = parser.errors.dup
      checked_blocks = 0
      skipped_blocks = 0

      if errors.empty?
        run_blocks.each do |block|
          normalized_shell = WorkflowChecks.normalize_shell(block.shell)
          unless normalized_shell
            skipped_blocks += 1
            next
          end

          errors.concat(
            @shellcheck_runner.check(
              block.script,
              shell: normalized_shell,
              source: block.source
            )
          )
          checked_blocks += 1
        end
      end

      CheckResult.new(files, run_blocks.length, checked_blocks, skipped_blocks, errors)
    end
  end

  class CLI
    def self.run(root: File.expand_path("..", __dir__), env: ENV, stdout: $stdout, stderr: $stderr)
      shellcheck = env.fetch("WORKFLOW_SHELLCHECK", "shellcheck")
      timeout = env.fetch("WORKFLOW_SHELLCHECK_TIMEOUT", DEFAULT_SHELLCHECK_TIMEOUT.to_s)
      runner = ShellcheckRunner.new(command: [shellcheck], timeout_seconds: timeout)
      result = Checker.new(root: root, shellcheck_runner: runner).run

      unless result.errors.empty?
        stderr.puts "Workflow validation failed:"
        result.errors.each { |error| stderr.puts "  - #{error}" }
        return 1
      end

      stdout.puts(
        "Validated #{result.files.length} GitHub Actions workflow files; " \
        "ShellCheck checked #{result.checked_blocks}/#{result.run_blocks} run blocks" \
        " (#{result.skipped_blocks} unsupported shell blocks skipped)."
      )
      0
    rescue Error, ArgumentError => e
      stderr.puts "Workflow validation failed:"
      stderr.puts "  - #{e.message}"
      1
    end
  end
end

exit WorkflowChecks::CLI.run if $PROGRAM_NAME == __FILE__
