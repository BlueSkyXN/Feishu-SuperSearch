#!/usr/bin/env ruby
# Dependency-free YAML syntax and top-level structure check for GitHub Actions.
require "psych"

root = File.expand_path("..", __dir__)
files = Dir[File.join(root, ".github", "workflows", "*.{yml,yaml}")].sort
abort "no workflow files found" if files.empty?

errors = []
files.each do |path|
  begin
    document = Psych.parse_file(path)
    mapping = document.root
    unless mapping.is_a?(Psych::Nodes::Mapping)
      errors << "#{path.sub(root + '/', '')}: workflow root must be a YAML mapping"
      next
    end

    keys = mapping.children.each_slice(2).map do |key_node, _value_node|
      key_node.value if key_node.is_a?(Psych::Nodes::Scalar)
    end.compact
    %w[name on jobs].each do |key|
      errors << "#{path.sub(root + '/', '')}: missing top-level #{key}:" unless keys.include?(key)
    end
  rescue Psych::SyntaxError => e
    errors << "#{path.sub(root + '/', '')}: #{e.message}"
  end
end

unless errors.empty?
  warn "Workflow validation failed:"
  errors.each { |error| warn "  - #{error}" }
  exit 1
end

puts "Validated #{files.length} GitHub Actions workflow files."
