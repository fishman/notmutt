source "https://rubygems.org"

# the docs site (docs/, GitHub Pages): local build matches Pages -
# remote_theme needs jekyll-remote-theme, and rouge is pinned to 4.x
# because jekyll 4.4.1 emits the Rouge::Formatters::HTMLLegacy
# deprecation on rouge 5
gem "jekyll", "~> 4.4"
gem "rouge", "~> 4.5"
group :jekyll_plugins do
  gem "jekyll-remote-theme"
  # just-the-docs declares jekyll-seo-tag and jekyll-include-cache,
  # which remote themes do not auto-install (Pages ships both in its
  # supported plugin set)
  gem "jekyll-seo-tag"
  gem "jekyll-include-cache"
end
