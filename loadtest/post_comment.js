import http from 'k6/http';
import { check, sleep } from 'k6';
import { SharedArray } from 'k6/data';
import { URLSearchParams } from 'https://jslib.k6.io/url/1.0.0/index.js'; // 引入k6的URLSearchParams库

// --- 配置选项 ---
export const options = {
    stages: [
        { duration: '10s', target: 500 }, // 线性增加到500个并发用户
        { duration: '40s', target: 500 }, // 维持500个并发用户
        { duration: '10s', target: 0 },  // 减少到0
    ],
    thresholds: {
        'http_req_duration': ['p(95)<100'], // 发表评论应该非常快，我们设定一个更严苛的目标
    },
};

// --- 初始化代码 ---
const tokens = new SharedArray('user tokens', function () {
    return open('./pressure_test_tokens.txt').split('\n').filter(Boolean);
});

const hotPostId = 1; // 假设ID为1的帖子是我们要压测的热门帖子

// --- 虚拟用户执行的默认函数 ---
export default function () {
    const token = tokens[Math.floor(Math.random() * tokens.length)];
    const url = 'http://127.0.0.1:4333/v3/send/comment';
    
    // 构造 x-www-form-urlencoded 格式的 payload
    const params = new URLSearchParams();
    params.append('pid', hotPostId);
    params.append('text', `token: ${token} k6 test comment at ${new Date().toISOString()}`);
    params.append('type', 'text');
    
    const requestParams = {
        headers: {
            'Content-Type': 'application/x-www-form-urlencoded',
            'TOKEN': token,
        },
    };

    // 发送POST请求
    const res = http.post(url, params.toString(), requestParams);

    // 检查点
    check(res, {
        'status is 200': (r) => r.status === 200,
        'response code is 0': (r) => r.json('code') === 0,
        'comment_id is returned': (r) => r.json('comment_id') > 0,
    });

    sleep(2); // 模拟用户评论后停留2秒
}