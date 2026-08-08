class Logg < Formula
  desc "One-command local log discovery, follow, and regex search for SREs"
  homepage "https://github.com/YOUR_GITHUB_USER/logg"
  url "https://github.com/YOUR_GITHUB_USER/logg/archive/refs/tags/v0.2.0.tar.gz"
  sha256 "REPLACE_WITH_RELEASE_TARBALL_SHA256"
  license "MIT"
  head "https://github.com/YOUR_GITHUB_USER/logg.git", branch: "main"

  depends_on "go" => :build

  def install
    ldflags = %W[-s -w -X main.version=#{version}]
    system "go", "build", *std_go_args(ldflags:), "."
  end

  test do
    assert_match "logg #{version}", shell_output("#{bin}/logg version")
    assert_match ".logg.conf", shell_output("#{bin}/logg config path")
  end
end
