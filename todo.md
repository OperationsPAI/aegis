我们接下来要设计一个loop形式的故障注入循环，你觉得怎么设计比较好？

我们现在是维持一个故障注入配置的池子，这个池子的配置从以下几点生成

1. 根据inject guided交互获取可以注入的点
2. 在service / pod /container level的选择，可以去查(a)相关{{代码仓库}}的loadgenerator来分析看哪个更适合注入,(b)clickhouse查询对应namespace的历史trace的各servicename的trace，这个代码仓库的地址可以从seed data里面获取
3. 如果是jvm相关的class method，根据代码仓库查找相应服务的代码，分析代码
4. 通过历史注入信息和配置的结果来获取反馈，我们更倾向于注入结果是algorithm.result.collection的相关配置，我们不要datapack.no_anomaly或者失败的相关配置
5. 在分析注入的时候，我们更倾向于不易被下游任务精准定位根因的配置
6. 也可以查找detector的结果，我们更倾向于detector定位到的api所属的service不是我们注入的service。