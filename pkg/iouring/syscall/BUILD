load("//tools:defs.bzl", "go_library")

package(
    default_visibility = ["//:sandbox"],
    licenses = ["notice"],
)

exports_files(["LICENSE"])

go_library(
    name = "syscall",
    srcs = [
        "io_uring_enter.go",
        "io_uring_register.go",
        "io_uring_setup.go",
        "types.go",
    ],
    marshal = False,
    stateify = False,
    visibility = ["//:sandbox"],
    deps = [
        "@org_golang_x_sys//unix:go_default_library",
    ],
)
