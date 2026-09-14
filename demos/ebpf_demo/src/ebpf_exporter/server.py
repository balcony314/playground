"""Prometheus 指标暴露服务（Flask）。

提供两个端点：

* ``/metrics``  输出 Prometheus 文本格式的全部指标，供 Prometheus 抓取
* ``/``         健康检查端点，返回 "ok"（供 K8s readinessProbe 使用）

监控模块只负责把观测数据写入 prometheus_client 的全局注册表，
本模块不关心指标如何产生，二者彻底解耦。
"""

from flask import Flask, Response
from prometheus_client import REGISTRY, CONTENT_TYPE_LATEST, generate_latest


def create_app() -> Flask:
    """创建 Flask 应用并注册指标路由。

    :returns: 配置好路由的 Flask 实例
    """
    app = Flask(__name__)

    @app.route("/metrics", methods=["GET"])
    def metrics() -> Response:
        """输出全局注册表中的全部 Prometheus 指标。"""
        return Response(generate_latest(REGISTRY), mimetype=CONTENT_TYPE_LATEST)

    @app.route("/", methods=["GET"])
    def ready() -> Response:
        """健康检查：进程存活即返回 ok。"""
        return Response("ok", mimetype="text/plain")

    return app


def serve(host: str, port: int) -> None:
    """阻塞式启动 HTTP 服务（生产使用建议前置 gunicorn 等 WSGI 服务器）。

    :param host: 监听地址，如 "0.0.0.0"
    :param port: 监听端口，如 9435
    """
    app = create_app()
    # debug=False：避免 Flask 调试器的热重载线程与 eBPF 事件循环互相干扰
    app.run(host=host, port=port, debug=False)
