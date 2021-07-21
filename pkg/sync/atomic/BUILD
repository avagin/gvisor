load("//tools:defs.bzl", "go_library", "go_test")
load("//tools/go_generics:defs.bzl", "go_template", "go_template_instance")

package(licenses = ["notice"])

go_template(
    name = "generic_atomic",
    srcs = ["generic_atomic.go"],
    types = [
        "Value",
    ],
    visibility = ["//:sandbox"],
)

go_template_instance(
    name = "atomic_int32",
    out = "atomicptr_int32.go",
    package = "atomic",
    suffix = "Int32",
    template = ":generic_atomic",
    types = {
        "Value": "int32",
    },
    substrs = {
        "ValueOp": "Int32",
    },
)

go_template_instance(
    name = "atomic_uint32",
    out = "atomicptr_uint32.go",
    package = "atomic",
    suffix = "Uint32",
    template = ":generic_atomic",
    types = {
        "Value": "uint32",
    },
    substrs = {
        "ValueOp": "Uint32",
    },
)

go_library(
    name = "atomic",
    srcs = [
        "atomicptr_int32.go",
        "atomicptr_uint32.go",
    ],
    visibility = ["//:sandbox"],
)

