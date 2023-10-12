load("//tools:defs.bzl", "go_test", "default_platform", "platform_capabilities", "platforms")

def all_platforms():
    """All platforms returns a list of all platforms."""
    available = dict(platforms.items())
    available[default_platform] = platforms.get(default_platform, [])
    return available.items()

def container_platform_tests(name, tags = [], **kwargs):
    for platform, platform_tags in all_platforms():
        go_test(
            name + "_" + platform + "_test",
            args = ["--test_platforms=" + platform],
            tags = ["runsc_" + platform] + platform_tags + tags,
            **kwargs,
        )
