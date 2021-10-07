load("//tools:defs.bzl", "go_library")

package(
    default_visibility = ["//:sandbox"],
    licenses = ["notice"],
)

exports_files(["LICENSE"])

go_library(
    name = "iouring",
    srcs = [
        "errors.go",
        "eventfd.go",
        "fixed_buffers.go",
        "fixed_files.go",
        "iouring.go",
        "link_request.go",
        "mmap.go",
        "options.go",
        "poller.go",
        "prep_request.go",
        "probe.go",
        "request.go",
        "timeout.go",
        "types.go",
        "user_data.go",
        "utils.go",
    ],
    marshal = False,
    stateify = False,
    visibility = ["//:sandbox"],
    deps = [
        "//pkg/iouring/syscall",
        "@org_golang_x_sys//unix:go_default_library",
    ],
)
