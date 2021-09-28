// Copyright 2018 The gVisor Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

#include <arpa/inet.h>
#include <fcntl.h>
#include <ifaddrs.h>
#include <linux/if.h>
#include <linux/netlink.h>
#include <linux/rtnetlink.h>
#include <sys/socket.h>
#include <sys/types.h>
#include <unistd.h>

#include <iostream>
#include <vector>

#include "gtest/gtest.h"
#include "absl/strings/str_format.h"
#include "test/syscalls/linux/socket_netlink_route_util.h"
#include "test/syscalls/linux/socket_netlink_util.h"
#include "test/util/capability_util.h"
#include "test/util/cleanup.h"
#include "test/util/file_descriptor.h"
#include "test/util/socket_util.h"
#include "test/util/test_util.h"

// Tests for NETLINK_ROUTE sockets.

namespace gvisor {
namespace testing {

namespace {

constexpr uint32_t kSeq = 12345;

using ::testing::_;
using ::testing::AnyOf;
using ::testing::Eq;

std::function<bool(int)> IsPositive() {
  return [](int val) { return val > 0; };
}

std::function<bool(int)> IsEqual(int target) {
  return [target](int val) { return val == target; };
}

// SetNeighRequest tests a RTM_NEWNEIGH + NLM_F_CREATE|NLM_F_REPLACE request.
TEST(NetlinkRouteTest, SetNeighRequest) {
  Link link = ASSERT_NO_ERRNO_AND_VALUE(LoopbackLink());

  FileDescriptor fd =
      ASSERT_NO_ERRNO_AND_VALUE(NetlinkBoundSocket(NETLINK_ROUTE));

  struct in_addr addr;
  ASSERT_EQ(inet_pton(AF_INET, "10.0.0.1", &addr), 1);

  char lladdr[6] = {0x01, 0, 0, 0, 0, 0};

  // Create should succeed, as no such neighbor in kernel.
  ASSERT_NO_ERRNO(NeighSet(link.index, AF_INET,
                           &addr, sizeof(addr), lladdr, sizeof(lladdr)));
}

// GetNeighDump tests a RTM_GETNEIGH + NLM_F_DUMP request.
TEST(NetlinkRouteTest, GetNeighDump) {
  FileDescriptor fd =
      ASSERT_NO_ERRNO_AND_VALUE(NetlinkBoundSocket(NETLINK_ROUTE));

  Link link = ASSERT_NO_ERRNO_AND_VALUE(LoopbackLink());
  uint32_t port = ASSERT_NO_ERRNO_AND_VALUE(NetlinkPortID(fd.get()));

  struct request {
    struct nlmsghdr hdr;
    struct ndmsg ndm;
    char buf[256];
  };

  struct request req = {};
  req.hdr.nlmsg_len = NLMSG_LENGTH(sizeof(struct ndmsg));
  req.hdr.nlmsg_type = RTM_GETNEIGH;
  req.hdr.nlmsg_flags = NLM_F_REQUEST | NLM_F_DUMP;
  req.hdr.nlmsg_seq = kSeq;
  req.ndm.ndm_family = AF_UNSPEC;

  bool verified = true;
  ASSERT_NO_ERRNO(NetlinkRequestResponse(
      fd, &req, sizeof(req),
      [&](const struct nlmsghdr* hdr) {
        // Validate the reponse to RTM_GETNEIGH + NLM_F_DUMP.
        EXPECT_THAT(hdr->nlmsg_type, AnyOf(Eq(RTM_NEWNEIGH), Eq(NLMSG_DONE)));

        EXPECT_TRUE((hdr->nlmsg_flags & NLM_F_MULTI) == NLM_F_MULTI)
            << std::hex << hdr->nlmsg_flags;

        EXPECT_EQ(hdr->nlmsg_seq, kSeq);
        EXPECT_EQ(hdr->nlmsg_pid, port);

        // The test should not proceed if it's not a RTM_NEWNEIGH message.
        if (hdr->nlmsg_type != RTM_NEWNEIGH) {
          return;
        }

        // RTM_NEWNEIGH contains at least the header and ndmsg.
        ASSERT_GE(hdr->nlmsg_len, NLMSG_SPACE(sizeof(struct ndmsg)));
        const struct ndmsg* msg =
            reinterpret_cast<const struct ndmsg*>(NLMSG_DATA(hdr));
        std::cout << "Found neighbor =" << msg->ndm_ifindex
                  << ", state=" << msg->ndm_state
                  << ", flags=" << msg->ndm_flags
                  << ", type=" << msg->ndm_type;

        int len = RTM_PAYLOAD(hdr);
        bool ndDstFound = false;

        for (struct rtattr* attr = RTM_RTA(msg); RTA_OK(attr, len);
             attr = RTA_NEXT(attr, len)) {
          if (attr->rta_type == NDA_DST) {
            char addr[INET_ADDRSTRLEN] = {};
            inet_ntop(AF_INET, RTA_DATA(attr), addr, sizeof(addr));
            std::cout << ", dst=" << addr;
            ndDstFound = true;
          }
        }

        std::cout << std::endl;

        verified = ndDstFound && verified;
      },
      false));
  // Found RTA_DST and RTA_LLADDR for each neighbour entry.
  //EXPECT_TRUE(found && verified);
}

// ReplaceNeighRequest tests a RTM_DELNEIGH request.
TEST(NetlinkRouteTest, DelNeighRequest) {
  Link link = ASSERT_NO_ERRNO_AND_VALUE(LoopbackLink());

  FileDescriptor fd =
      ASSERT_NO_ERRNO_AND_VALUE(NetlinkBoundSocket(NETLINK_ROUTE));

  struct in_addr addr;
  //ASSERT_EQ(inet_pton(AF_INET, "10.0.0.1", &addr), 1);
  ASSERT_EQ(inet_pton(AF_INET, "0.0.0.0", &addr), 1);

  char lladdr[6] = {0x01, 0, 0, 0, 0, 0};

  // Create should succeed, as no such neighbor in kernel.
  ASSERT_NO_ERRNO(NeighSet(link.index, AF_INET,
                           &addr, sizeof(addr), lladdr, sizeof(lladdr)));
  ASSERT_NO_ERRNO(NeighDel(link.index, AF_INET, &addr, sizeof(addr)));
}

}  // namespace

}  // namespace testing
}  // namespace gvisor
