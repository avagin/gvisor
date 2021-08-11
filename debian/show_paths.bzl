def format(target):
    provider_map = providers(target)

    # These are left to let you see the technique.
    # print(provider_map.keys())
    # print(provider_map["//third_party/bazel_rules/rules_pkg/pkg:providers.bzl%PackageArtifactInfo"])
    # print(provider_map["OutputGroupInfo"])
    return "\n".join([
        provider_map["OutputGroupInfo"].out.to_list()[0].path,
        provider_map["OutputGroupInfo"].deb.to_list()[0].path,
        provider_map["OutputGroupInfo"].changes.to_list()[0].path,
    ])
