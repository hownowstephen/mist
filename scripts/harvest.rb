# Harvests (template, data) pairs from Shopify/liquid-spec, jg-rp/golden-liquid and
# Shopify/liquid's test suite into JSON. liquidjs is the oracle (see oracle.mjs); Ruby
# Liquid's expected output is kept as `ruby` where the source has one, as a hint of where
# liquidjs may change, and never decides pass/fail.
#
#   ruby harvest.rb LIQUID_SPEC_DIR GOLDEN_LIQUID_DIR SHOPIFY_LIQUID_DIR OUT.json
require "yaml"
require "json"

spec_dir, golden_dir, liquid_dir, out = ARGV
abort "usage: ruby harvest.rb LIQUID_SPEC_DIR GOLDEN_LIQUID_DIR SHOPIFY_LIQUID_DIR OUT.json" unless out

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

# Whether Ruby's output is comparable: JSON drops 1.0's floatness, liquid-spec's
# `instantiate:` keys are drops to Ruby, and Ruby's clock differs from oracle.mjs's.
def comparable(ruby, tpl, data)
  floats = ->(v) { v.is_a?(Float) ? v == v.to_i : v.is_a?(Hash) ? v.values.any?(&floats) : v.is_a?(Array) && v.any?(&floats) }
  ruby.is_a?(String) && jsonable(ruby) && !floats.(data) && !JSON.generate(data).include?("instantiate:") &&
    !"#{tpl}#{JSON.generate(data)}".match?(/["'](now|today)["']/)
end

$cases = []
$seen = {}
def add(src, name, tpl, data, ruby = nil)
  data ||= {}
  return unless jsonable(tpl) && tpl.is_a?(String) && data.is_a?(Hash) && jsonable(data)
  key = [tpl, data].hash
  return if $seen[key]
  $seen[key] = true
  c = { "src" => src, "name" => name.to_s, "tpl" => tpl.dup.force_encoding("UTF-8"), "data" => data }
  c["ruby"] = ruby.dup.force_encoding("UTF-8") if comparable(ruby, tpl, data)
  $cases << c
end

# Features name what Ruby's harness adds beyond plain Liquid (drops, Ruby types, Shopify's
# filters), and render_errors writes errors inline where liquidjs throws.
def spec_ruby(suite, s)
  features = Array(suite["features"]) + Array(s["features"])
  return if (features.map(&:to_s) - %w[lax_parsing inline_errors]).any?
  return if s.fetch("render_errors", suite.dig("defaults", "render_errors")) && s["expected"].to_s.match?(/Liquid (syntax )?error/)
  s["expected"]
end

Dir.chdir(File.join(spec_dir, "specs")) do
  Dir["**/*.yml"].sort.each do |f|
    next if f.start_with?("benchmarks/") || f.end_with?("suite.yml")
    d = YAML.unsafe_load_file(f)
    specs = d.is_a?(Hash) ? d["specs"] : d
    suite = YAML.unsafe_load_file("#{f.split("/").first}/suite.yml")
    (specs || []).each { |s| add("liquid-spec/#{f}", s["name"], s["template"], s["environment"], spec_ruby(suite, s)) }
  end
  # directory-per-spec layout (shopify_theme_dawn)
  Dir["**/template.liquid"].sort.each do |f|
    dir = File.dirname(f)
    env = File.exist?("#{dir}/environment.yml") ? YAML.unsafe_load_file("#{dir}/environment.yml") : {}
    add("liquid-spec/#{dir}", dir, File.read(f), env) # Ruby's output needs Shopify's theme objects and filters
  end
end

# Tests with partials (`templates`) need a file system liquidjs's oracle doesn't have.
# golden-liquid checks `result` against Shopify/liquid (golden_liquid.rb).
JSON.parse(File.read(File.join(golden_dir, "golden_liquid.json"), encoding: "UTF-8"))["tests"].each do |t|
  add("golden-liquid", t["name"], t["template"], t["data"], t["result"]) unless t["templates"]
end

# Record every template the Ruby test suite renders, instead of asserting.
$LOAD_PATH.unshift(File.join(liquid_dir, "test"), File.join(liquid_dir, "lib"))
ENV["MT_NO_EXPECTATIONS"] = "1"
require "minitest"
require "test_helper"
module Minitest::Assertions
  def assert_template_result(expected, template, assigns = {}, render_errors: false, **_)
    add("shopify/liquid/#{self.class}", name, template, assigns, (expected unless render_errors))
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
