package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

var cidr string
var port int
var ipPool *IPv6Pool

// IPv6Pool 管理有效的IPv6地址池
type IPv6Pool struct {
	sync.RWMutex
	validIPs   []string
	blacklist  map[string]bool
	checkURL   string
	checkCount int
	timeout    time.Duration
}

// NewIPv6Pool 创建新的IPv6地址池
func NewIPv6Pool(checkURL string, checkCount int, timeout time.Duration) *IPv6Pool {
	return &IPv6Pool{
		validIPs:   make([]string, 0, checkCount),
		blacklist:  make(map[string]bool),
		checkURL:   checkURL,
		checkCount: checkCount,
		timeout:    timeout,
	}
}

// GetRandomIP 获取一个随机的有效IPv6地址
func (p *IPv6Pool) GetRandomIP() (string, error) {
	p.RLock()
	defer p.RUnlock()

	if len(p.validIPs) == 0 {
		return "", fmt.Errorf("no valid IPv6 addresses available")
	}

	// 从有效IP池中随机选择一个
	index := randomInt(len(p.validIPs))
	return p.validIPs[index], nil
}

// IsBlacklisted 检查IP是否在黑名单中
func (p *IPv6Pool) IsBlacklisted(ip string) bool {
	p.RLock()
	defer p.RUnlock()
	return p.blacklist[ip]
}

// AddToBlacklist 将IP添加到黑名单
func (p *IPv6Pool) AddToBlacklist(ip string) {
	p.Lock()
	defer p.Unlock()
	p.blacklist[ip] = true

	// 同时从有效IP列表中移除
	for i, validIP := range p.validIPs {
		if validIP == ip {
			p.validIPs = append(p.validIPs[:i], p.validIPs[i+1:]...)
			break
		}
	}
	log.Printf("已将IP添加到黑名单: %s，剩余有效IP数量: %d", ip, len(p.validIPs))
}

// AddToValidPool 将IP添加到有效池
func (p *IPv6Pool) AddToValidPool(ip string) {
	p.Lock()
	defer p.Unlock()

	// 检查是否已经在黑名单中
	if p.blacklist[ip] {
		return
	}

	// 检查是否已在有效池中
	for _, validIP := range p.validIPs {
		if validIP == ip {
			return
		}
	}

	p.validIPs = append(p.validIPs, ip)
	log.Printf("已将IP添加到有效池: %s，当前有效IP总数: %d", ip, len(p.validIPs))
}

// checkIPv6 检查IPv6地址是否可用
func (p *IPv6Pool) checkIPv6(ip string) bool {
	// 为 IPv6 地址添加方括号
	bracketedIP := "[" + ip + "]"

	// 使用指定的出口 IP 地址创建连接
	localAddr, err := net.ResolveTCPAddr("tcp", bracketedIP+":0")
	if err != nil {
		log.Printf("解析本地地址错误: %v", err)
		return false
	}

	dialer := net.Dialer{
		LocalAddr: localAddr,
		Timeout:   p.timeout,
	}

	// 创建自定义客户端
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: dialer.DialContext,
		},
		Timeout: p.timeout,
	}

	// 创建上下文
	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()

	// 创建请求
	req, err := http.NewRequestWithContext(ctx, "GET", p.checkURL, nil)
	if err != nil {
		log.Printf("创建请求错误: %v", err)
		return false
	}

	// 发送请求
	startTime := time.Now()
	resp, err := client.Do(req)
	elapsedTime := time.Since(startTime)

	if err != nil {
		log.Printf("检查IPv6 %s 失败: %v (%.2f秒)", ip, err, elapsedTime.Seconds())
		return false
	}
	defer resp.Body.Close()

	// 检查响应状态码
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Printf("IPv6 %s 可用，响应时间: %.2f秒", ip, elapsedTime.Seconds())
		return true
	}

	log.Printf("IPv6 %s 返回状态码 %d (%.2f秒)", ip, resp.StatusCode, elapsedTime.Seconds())
	return false
}

// PreCheckIPs 预检IP地址
func (p *IPv6Pool) PreCheckIPs(cidr string, count int) {
	log.Printf("开始预检 %d 个IPv6地址...", count)

	var wg sync.WaitGroup
	var mutex sync.Mutex
	checkedCount := 0
	validCount := 0

	// 创建通道用于传递待检查的IP
	ipChan := make(chan string, count)

	// 生成待检查的IP
	go func() {
		for i := 0; i < count*2; i++ { // 生成2倍的IP以增加成功率
			ip, err := generateRandomIPv6(cidr)
			if err != nil {
				log.Printf("生成随机IPv6错误: %v", err)
				continue
			}
			ipChan <- ip
		}
		close(ipChan)
	}()

	// 启动多个goroutine进行并行检查
	workers := 10 // 并行检查的goroutine数量
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()

			for ip := range ipChan {
				mutex.Lock()
				// 如果已经检查够了指定数量的有效IP，直接退出
				if validCount >= p.checkCount {
					mutex.Unlock()
					return
				}
				// 增加已检查计数
				checkedCount++
				currentChecked := checkedCount
				mutex.Unlock()

				// 检查IP
				isValid := p.checkIPv6(ip)

				if isValid {
					p.AddToValidPool(ip)

					mutex.Lock()
					validCount++

					// 检查是否已经找到足够数量的有效IP
					foundEnough := validCount >= p.checkCount
					currentValid := validCount
					mutex.Unlock()

					log.Printf("找到有效IP %d/%d (已检查 %d 个)", currentValid, p.checkCount, currentChecked)

					if foundEnough {
						return
					}
				} else {
					p.AddToBlacklist(ip)
				}
			}
		}()
	}

	wg.Wait()

	log.Printf("预检完成: 在检查的 %d 个IPv6地址中找到 %d 个有效地址", checkedCount, len(p.validIPs))
}

// randomInt 生成指定范围内的随机整数
func randomInt(max int) int {
	b := make([]byte, 4)
	rand.Read(b)
	return int(b[0]) % max
}

func main() {
	checkURL := flag.String("check-url", "https://partyrock.aws", "用于检查IPv6可用性的URL")
	checkCount := flag.Int("check-count", 50, "预检的IPv6地址数量")
	checkTimeout := flag.Duration("check-timeout", 5*time.Second, "IP可用性检查超时时间")

	flag.IntVar(&port, "port", 52122, "服务器端口")
	flag.StringVar(&cidr, "cidr", "", "IPv6 CIDR")
	flag.Parse()

	if cidr == "" {
		log.Fatal("CIDR为空")
	}

	httpPort := port
	socks5Port := port + 1

	if socks5Port > 65535 {
		log.Fatal("端口号过大")
	}

	// 初始化IP池并预检IPv6地址
	ipPool = NewIPv6Pool(*checkURL, *checkCount, *checkTimeout)

	log.Printf("开始使用 %s 进行IPv6地址预检", *checkURL)
	ipPool.PreCheckIPs(cidr, *checkCount)

	// 检查是否找到了有效的IP
	if len(ipPool.validIPs) == 0 {
		log.Fatal("预检过程中未找到有效的IPv6地址")
	}

	log.Printf("成功预检了 %d 个IPv6地址", len(ipPool.validIPs))

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		err := socks5Server.ListenAndServe("tcp", fmt.Sprintf("0.0.0.0:%d", socks5Port))
		if err != nil {
			log.Fatal("SOCKS5服务器错误:", err)
		}

	}()
	go func() {
		err := http.ListenAndServe(fmt.Sprintf("0.0.0.0:%d", httpPort), httpProxy)
		if err != nil {
			log.Fatal("HTTP服务器错误", err)
		}
	}()

	log.Println("服务器正在运行...")
	log.Printf("HTTP代理运行在 0.0.0.0:%d", httpPort)
	log.Printf("SOCKS5代理运行在 0.0.0.0:%d", socks5Port)
	log.Printf("IPv6 CIDR:[%s]", cidr)
	log.Printf("使用 %d 个预检通过的有效IPv6地址", len(ipPool.validIPs))
	wg.Wait()

}

func generateRandomIPv6(cidr string) (string, error) {
	// 解析CIDR
	_, ipv6Net, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", err
	}

	// 获取网络部分和掩码长度
	maskSize, _ := ipv6Net.Mask.Size()

	// 计算随机部分的长度
	randomPartLength := 128 - maskSize

	// 生成随机部分
	randomPart := make([]byte, randomPartLength/8)
	_, err = rand.Read(randomPart)
	if err != nil {
		return "", err
	}

	// 获取网络部分
	networkPart := ipv6Net.IP.To16()

	// 合并网络部分和随机部分
	for i := 0; i < len(randomPart); i++ {
		networkPart[16-len(randomPart)+i] = randomPart[i]
	}

	return networkPart.String(), nil
}

// GetIPFromPool 从IP池中获取一个随机的有效IPv6地址，如果池为空则生成新的
func GetIPFromPool() (string, error) {
	if ipPool == nil {
		// 如果IP池未初始化，直接生成随机IP
		return generateRandomIPv6(cidr)
	}

	// 尝试从有效池中获取IP
	ip, err := ipPool.GetRandomIP()
	if err == nil {
		return ip, nil
	}

	// 如果有效池为空，生成新IP
	log.Println("有效IP池为空，正在生成随机IP")
	return generateRandomIPv6(cidr)
}
