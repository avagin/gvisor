load("//tools:arch.bzl", "arch_genrule")
load("//tools:defs.bzl", "go_library")

package(
    default_applicable_licenses = ["//:license"],
    licenses = ["notice"],
)

arch_genrule(
    name = "vdso_arch",
    src = "//vdso",
    template = "vdso_%s.so",
)

go_library(
    name = "vdsodata",
    srcs = [
        "vdsodata.go",
    ] + select({
        "//tools/bazeldefs:amd64": ["vdsodata_amd64.go"],
        "//tools/bazeldefs:arm64": ["vdsodata_arm64.go"],
        "//conditions:default": [
            "vdsodata_amd64.go",
            "vdsodata_arm64.go",
        ],
    }),
    embedsrcs = [
        ":vdso_arch",  # keep
    ],

    marshal = False,
    stateify = False,
    visibility = ["//pkg/sentry:internal"],
)
