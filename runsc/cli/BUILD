load("//tools:defs.bzl", "go_library", "go_test")

package(
    default_applicable_licenses = ["//:license"],
    licenses = ["notice"],
)

go_library(
    name = "cli",
    srcs = ["main.go"],
    visibility = [
        "//:__pkg__",
        "//runsc:__pkg__",
    ],
    deps = [
        "//pkg/coverage",
        "//pkg/log",
        "//pkg/refs",
        "//pkg/sentry/platform",
        "//pkg/sentry/syscalls/linux",
        "//pkg/tcpip/nftables",
        "//runsc/cmd",
        "//runsc/cmd/nvproxy",
        "//runsc/cmd/trace",
        "//runsc/cmd/util",
        "//runsc/config",
        "//runsc/flag",
        "//runsc/specutils",
        "//runsc/starttime",
        "//runsc/version",
        "@com_github_google_subcommands//:go_default_library",
        "@org_golang_x_sys//unix:go_default_library",
    ],
)

go_test(
    name = "cli_test",
    srcs = ["cli_test.go"],
    library = ":cli",
    deps = [
        "//runsc/cmd",
        "//runsc/flag",
        "@com_github_google_subcommands//:go_default_library",
    ],
)
