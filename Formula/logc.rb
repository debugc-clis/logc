class Logc < Formula
  desc "One-command local log discovery, follow, and regex search for SREs"
  homepage "https://logc.com"
  url "https://github.com/zchensh/logc/archive/refs/tags/v0.2.0.tar.gz"
  sha256 "REPLACE_WITH_RELEASE_TARBALL_SHA256"
  license "MIT"
  head "https://github.com/zchensh/logc.git", branch: "main"

  depends_on "go" => :build

  def install
    ldflags = %W[-s -w -X main.version=#{version}]
    system "go", "build", *std_go_args(ldflags:), "."
  end

  test do
    assert_match "logc #{version}", shell_output("#{bin}/logc version")
    assert_match ".logc.conf", shell_output("#{bin}/logc config path")
  end
end
