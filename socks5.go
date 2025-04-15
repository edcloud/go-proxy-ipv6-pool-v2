package main

import (
	"context"
	"log"
	"net"
	"time"

	socks5 "github.com/armon/go-socks5"
)

var socks5Conf = &socks5.Config{}
var socks5Server *socks5.Server

func init() {
	// 指定出口 IP 地址
	// 指定本地出口 IPv6 地址

	// 创建一个 SOCKS5 服务器配置
	socks5Conf = &socks5.Config{
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// 使用 IP 池中的 IP 而不是生成新的
			outgoingIP, err := GetIPFromPool()
			if err != nil {
				log.Printf("[SOCKS5] 从IP池获取IP错误: %v", err)
				return nil, err
			}
			outgoingIP = "[" + outgoingIP + "]"

			// 使用指定的出口 IP 地址创建连接
			localAddr, err := net.ResolveTCPAddr("tcp", outgoingIP+":0")
			if err != nil {
				log.Printf("[SOCKS5] 解析本地地址错误: %v", err)
				return nil, err
			}
			dialer := net.Dialer{
				LocalAddr: localAddr,
				Timeout:   10 * time.Second, // 添加超时设置
			}
			// 通过代理服务器建立到目标服务器的连接

			log.Printf("[SOCKS5] 通过 %s 连接到 %s", outgoingIP, addr)
			startTime := time.Now()
			conn, err := dialer.DialContext(ctx, network, addr)
			elapsedTime := time.Since(startTime)

			if err != nil {
				log.Printf("[SOCKS5] 通过 %s 连接到 %s 失败: %v (%.2f秒)", outgoingIP, addr, err, elapsedTime.Seconds())
				// 如果连接失败，将该 IP 加入黑名单
				if ipPool != nil {
					ip := outgoingIP[1 : len(outgoingIP)-1] // 移除方括号
					ipPool.AddToBlacklist(ip)
				}
				return nil, err
			}

			log.Printf("[SOCKS5] 通过 %s 成功连接到 %s，耗时 %.2f秒", outgoingIP, addr, elapsedTime.Seconds())
			return conn, nil
		},
		// 添加超时处理
		Logger: log.New(log.Writer(), "[SOCKS5] ", log.LstdFlags),
	}
	var err error
	// 创建 SOCKS5 服务器
	socks5Server, err = socks5.New(socks5Conf)
	if err != nil {
		log.Fatal(err)
	}
}
