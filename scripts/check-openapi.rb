#!/usr/bin/env ruby
require "psych"

root = File.expand_path("..", __dir__)
path = File.join(root, "api", "openapi.yaml")
errors = []

def mapping_entries(node)
  node.children.each_slice(2).map { |key, value| [key, value] }
end

def scalar_key(node)
  node.value if node.is_a?(Psych::Nodes::Scalar)
end

def check_duplicates(node, location, errors)
  case node
  when Psych::Nodes::Mapping
    seen = {}
    mapping_entries(node).each do |key_node, value_node|
      key = scalar_key(key_node)
      if key
        errors << "#{location}: duplicate mapping key #{key.inspect}" if seen[key]
        seen[key] = true
      end
      check_duplicates(value_node, key ? "#{location}.#{key}" : location, errors)
    end
  when Psych::Nodes::Sequence
    node.children.each_with_index { |child, index| check_duplicates(child, "#{location}[#{index}]", errors) }
  end
end

begin
  document = Psych.parse_file(path)
  mapping = document.root
  unless mapping.is_a?(Psych::Nodes::Mapping)
    errors << "api/openapi.yaml: root must be a YAML mapping"
  else
    check_duplicates(mapping, "openapi", errors)
    entries = mapping_entries(mapping).to_h { |key, value| [scalar_key(key), value] }
    version = entries["openapi"]
    unless version.is_a?(Psych::Nodes::Scalar) && version.value.start_with?("3.1")
      errors << "api/openapi.yaml: openapi must declare version 3.1.x"
    end
    %w[info paths components].each do |key|
      errors << "api/openapi.yaml: missing top-level #{key}:" unless entries[key].is_a?(Psych::Nodes::Mapping)
    end
  end
rescue Psych::SyntaxError => e
  errors << "api/openapi.yaml: #{e.message}"
end

unless errors.empty?
  warn "OpenAPI validation failed:"
  errors.each { |error| warn "  - #{error}" }
  exit 1
end

puts "Validated api/openapi.yaml syntax, structure, and unique mapping keys."
