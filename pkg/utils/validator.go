package utils

import (
	"fmt"
	"strings"
)

func ValidatePodCidr(cidr, ip string) error {
	for _, cidrBlock := range strings.Split(cidr, ",") {
		for _, ipAddr := range strings.Split(ip, ",") {
			if CheckProtocol(cidrBlock) != CheckProtocol(ipAddr) {
				continue
			}

			ipStr := IPToString(ipAddr)
			if SubnetBroadcast(cidrBlock) == ipStr {
				return fmt.Errorf("%s is the broadcast ip in cidr %s", ipStr, cidrBlock)
			}
			if SubnetNumber(cidrBlock) == ipStr {
				return fmt.Errorf("%s is the network number ip in cidr %s", ipStr, cidrBlock)
			}
		}
	}
	return nil
}
