package main

import (
	"io"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/elazarl/goproxy"
)

var httpProxy = goproxy.NewProxyHttpServer()

func init() {
	httpProxy.Verbose = true

	httpProxy.OnRequest().DoFunc(
		func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
			// 使用 IP 池中的 IP 而不是生成新的
			outgoingIP, err := GetIPFromPool()
			if err != nil {
				log.Printf("从IP池获取IP错误: %v", err)
				return req, nil
			}
			outgoingIP = "[" + outgoingIP + "]"
			// 使用指定的出口 IP 地址创建连接
			localAddr, err := net.ResolveTCPAddr("tcp", outgoingIP+":0")
			if err != nil {
				log.Printf("[HTTP] 解析本地地址错误: %v", err)
				return req, nil
			}
			dialer := net.Dialer{
				LocalAddr: localAddr,
				Timeout:   10 * time.Second, // 添加超时设置
			}

			// 通过代理服务器建立到目标服务器的连接
			// 发送 http 请求
			// 使用自定义拨号器设置 HTTP 客户端
			// 创建新的 HTTP 请求

			newReq, err := http.NewRequest(req.Method, req.URL.String(), req.Body)
			if err != nil {
				log.Printf("[HTTP] 创建新请求错误: %v", err)
				return req, nil
			}
			newReq.Header = req.Header

			// 设置自定义拨号器的 HTTP 客户端
			client := &http.Client{
				Transport: &http.Transport{
					DialContext: dialer.DialContext,
				},
				Timeout: 30 * time.Second, // 添加整体请求超时
			}

			// 发送 HTTP 请求
			log.Printf("[HTTP] 通过 %s 向 %s 发送请求", outgoingIP, req.URL.Host)
			startTime := time.Now()
			resp, err := client.Do(newReq)
			elapsedTime := time.Since(startTime)

			if err != nil {
				log.Printf("[HTTP] 发送请求错误: %v (%.2f秒)", err, elapsedTime.Seconds())
				// 如果请求失败，将该 IP 加入黑名单
				if ipPool != nil {
					ip := outgoingIP[1 : len(outgoingIP)-1] // 移除方括号
					ipPool.AddToBlacklist(ip)
				}
				return req, nil
			}

			log.Printf("[HTTP] 通过 %s 向 %s 的请求完成，耗时 %.2f秒", outgoingIP, req.URL.Host, elapsedTime.Seconds())
			return req, resp
		},
	)

	httpProxy.OnRequest().HijackConnect(
		func(req *http.Request, client net.Conn, ctx *goproxy.ProxyCtx) {
			// 使用 IP 池中的 IP 而不是生成新的
			outgoingIP, err := GetIPFromPool()
			if err != nil {
				log.Printf("从IP池获取IP错误: %v", err)
				return
			}
			outgoingIP = "[" + outgoingIP + "]"
			// 使用指定的出口 IP 地址创建连接
			localAddr, err := net.ResolveTCPAddr("tcp", outgoingIP+":0")
			if err != nil {
				log.Printf("[HTTP] 解析本地地址错误: %v", err)
				return
			}
			dialer := net.Dialer{
				LocalAddr: localAddr,
				Timeout:   10 * time.Second, // 添加超时设置
			}

			// 通过代理服务器建立到目标服务器的连接
			log.Printf("[HTTP] 通过 %s 建立到 %s 的CONNECT连接", outgoingIP, req.URL.Host)
			startTime := time.Now()
			server, err := dialer.Dial("tcp", req.URL.Host)
			elapsedTime := time.Since(startTime)

			if err != nil {
				log.Printf("[HTTP] 通过 %s 连接到 %s 失败: %v (%.2f秒)", outgoingIP, req.URL.Host, err, elapsedTime.Seconds())
				// 如果连接失败，将该 IP 加入黑名单
				if ipPool != nil {
					ip := outgoingIP[1 : len(outgoingIP)-1] // 移除方括号
					ipPool.AddToBlacklist(ip)
				}
				client.Write([]byte("HTTP/1.1 500 Internal Server Error\r\n\r\n"))
				client.Close()
				return
			}

			log.Printf("[HTTP] 通过 %s 成功建立到 %s 的连接，耗时 %.2f秒", outgoingIP, req.URL.Host, elapsedTime.Seconds())

			// 响应客户端连接已建立
			client.Write([]byte("HTTP/1.0 200 OK\r\n\r\n"))
			// 从客户端复制数据到目标服务器
			go func() {
				defer server.Close()
				defer client.Close()
				io.Copy(server, client)
			}()

			// 从目标服务器复制数据到客户端
			go func() {
				defer server.Close()
				defer client.Close()
				io.Copy(client, server)
			}()

		},
	)
}
