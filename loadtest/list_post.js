import http from 'k6/http';
import { check, sleep } from 'k6';
import { SharedArray } from 'k6/data';

// --- 配置选项 ---
export const options = {
    // 定义虚拟用户数(VUs)和测试时长
    stages: [
        { duration: '20s', target: 1000 }, // 20秒内从0 VU线性增加到1000 VU
        { duration: '1m', target: 800 },  // 维持800 VU运行1分钟
        { duration: '20s', target: 0 },  // 10秒内线性减少到0 VU
    ],
    // 定义性能阈值，测试不达标时会失败
    thresholds: {
        'http_req_failed': ['rate<0.01'], // http错误率应小于1%
        'http_req_duration': ['p(95)<200'], // 95%的请求响应时间应小于200ms
    },
};

// --- 初始化代码 ---
// 使用SharedArray高效地在VUs之间共享tokens数据
// 这是在测试开始前每个VU只读一次的最佳实践
const tokens = new SharedArray('user tokens', function () {
    // k6/data 'open' 函数在init上下文中执行
    return open('./pressure_test_tokens.txt').split('\n').filter(Boolean);
});


// --- 虚拟用户执行的默认函数 ---
export default function () {
    // 1. 从共享数组中随机选择一个token
    const token = tokens[Math.floor(Math.random() * tokens.length)];
    const page = Math.floor(Math.random() * 10) + 1; // 随机请求前10页

    const url = `http://127.0.0.1:4333/v3/contents/post/list?page=${page}`;
    const params = {
        headers: {
            'TOKEN': token,
        },
    };

    // 2. 发送HTTP GET请求
    const res = http.get(url, params);

    // 3. 检查点(Check): 验证响应是否正确
    check(res, {
        'is status 200': (r) => r.status === 200,
        'response code is 0': (r) => r.json('code') === 0,
    });

    // 4. 思考时间: 模拟真实用户在浏览页面时会停留片刻
    sleep(1); // 每个VU在完成一次请求后等待1秒
}