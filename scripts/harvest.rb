# Harvests (template, data) pairs from Shopify/liquid-spec and Shopify/liquid's
# test suite into JSON on stdout. Expected outputs are not kept: liquidjs is the
# oracle (see oracle.mjs), since Ruby Liquid differs from it.
#
#   ruby harvest.rb LIQUID_SPEC_DIR SHOPIFY_LIQUID_DIR OUT.json
require "yaml"
require "json"

spec_dir, liquid_dir, out = ARGV
abort "usage: ruby harvest.rb LIQUID_SPEC_DIR SHOPIFY_LIQUID_DIR OUT.json" unless out

# Plain JSON data only; drops, symbols, ranges etc. can't cross to liquidjs.
def jsonable(v, depth = 0)
  return false if depth > 64 # self-referential YAML anchors
  case v
  when Hash then v.all? { |k, x| k.is_a?(String) && jsonable(x, depth + 1) }
  when Array then v.all? { |x| jsonable(x, depth + 1) }
  when String then v.dup.force_encoding("UTF-8").valid_encoding?
  when true, false, nil, Integer then true
  when Float then v.finite?
  else false
  end
end

$cases = []
$seen = {}
def add(src, name, tpl, data)
  data ||= {}
  return unless jsonable(tpl) && tpl.is_a?(String) && data.is_a?(Hash) && jsonable(data)
  key = [tpl, data].hash
  return if $seen[key]
  $seen[key] = true
  $cases << { "src" => src, "name" => name.to_s, "tpl" => tpl.dup.force_encoding("UTF-8"), "data" => data }
end

Dir.chdir(File.join(spec_dir, "specs")) do
  Dir["**/*.yml"].sort.each do |f|
    next if f.start_with?("benchmarks/") || f.end_with?("suite.yml")
    d = YAML.unsafe_load_file(f)
    specs = d.is_a?(Hash) ? d["specs"] : d
    (specs || []).each { |s| add("liquid-spec/#{f}", s["name"], s["template"], s["environment"]) }
  end
  # directory-per-spec layout (shopify_theme_dawn)
  Dir["**/template.liquid"].sort.each do |f|
    dir = File.dirname(f)
    env = File.exist?("#{dir}/environment.yml") ? YAML.unsafe_load_file("#{dir}/environment.yml") : {}
    add("liquid-spec/#{dir}", dir, File.read(f), env)
  end
end

# Record every template the Ruby test suite renders, instead of asserting.
$LOAD_PATH.unshift(File.join(liquid_dir, "test"), File.join(liquid_dir, "lib"))
ENV["MT_NO_EXPECTATIONS"] = "1"
require "minitest"
require "test_helper"
module Minitest::Assertions
  def assert_template_result(_expected, template, assigns = {}, **_)
    add("shopify/liquid/#{self.class}", name, template, assigns)
  end

  def assert_match_syntax_error(_match, template, **_)
    add("shopify/liquid/#{self.class}", name, template, {})
  end

  def assert_syntax_error(template, **_)
    add("shopify/liquid/#{self.class}", name, template, {})
  end
end
Dir[File.join(liquid_dir, "test/integration/**/*_test.rb")].sort.each do |f|
  require f
rescue LoadError => e
  warn "skipping #{f}: #{e.message}" # needs a gem we don't install
end
Minitest.run(["--seed", "1"]) # failures are expected; the helpers no longer assert
File.write(out, JSON.generate($cases))
