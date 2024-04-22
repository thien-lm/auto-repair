package vcd

import (
    "fmt"
    "net/url"
    "github.com/vmware/go-vcloud-director/v2/govcd"
)

type Config struct {
    User     string
    Password string
    Org      string
    Href     string
    VDC      string
    Insecure bool
}
//Example config
// User:     "xplat-demo_kixhhgke@fptcloud.com",
// Password: "7U603!,xbq{K{YcW",
// Org:      "XPLAT-DEMO-SGN10-ORG",
// Href:     "https://sgn10.fptcloud.com/api",
// VDC:      "XPLAT-DEMO-SGN10-VPC",
// Insecure: true,
//

func (c *Config) NewClient() (*govcd.VCDClient, error) {
    u, err := url.ParseRequestURI(c.Href)
    if err != nil {
        return nil, fmt.Errorf("unable to pass url: %s", err)
    }
    vcdclient := govcd.NewVCDClient(*u, c.Insecure)
    err = vcdclient.Authenticate(c.User, c.Password, c.Org)
    if err != nil {
        return nil, fmt.Errorf("unable to authenticate: %s", err)
    }
    return vcdclient, nil
}
