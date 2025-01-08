package main

import (
	"io"
	"log"
	"net/http"
	"net/url"
)

func handleRequestAndRedirect(res http.ResponseWriter, req *http.Request) {
	// 解析出目标服务器的URL
	url, err := url.Parse(req.RequestURI)
	if err != nil {
		http.Error(res, err.Error(), http.StatusBadRequest)
		return
	}

	// 创建一个新的请求，用于向目标服务器发送
	outReq := new(http.Request)
	*outReq = *req // 复制请求的内容

	outReq.URL = url
	outReq.URL.Scheme = req.URL.Scheme
	outReq.URL.Host = req.URL.Host

	// 发送请求
	resp, err := http.DefaultTransport.RoundTrip(outReq)
	if err != nil {
		http.Error(res, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// 将目标服务器返回的头信息复制到响应中
	for key, value := range resp.Header {
		for _, v := range value {
			res.Header().Add(key, v)
		}
	}

	// 设置响应状态码
	res.WriteHeader(resp.StatusCode)

	// 将目标服务器返回的内容复制到响应体中
	io.Copy(res, resp.Body)
}

func main() {
	// 设置监听的端口
	http.HandleFunc("/", handleRequestAndRedirect)
	err := http.ListenAndServe("127.0.0.1:1180", nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
