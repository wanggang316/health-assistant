# 餐食拍照识别：模型与数据源选型调研

调研日期：2026-11-21。价格、模型 ID 均来自厂商官方页面（见文末来源）。无法核实的内容标记为 **TODO**。

## 0. 直接结论

| 场景 | Primary | Fallback | 低成本档 / 降级 |
| --- | --- | --- | --- |
| (a) 海外后端 | `gemini-3.8-flash`（thinking 调低） | `gpt-6.1-terra` | `gemini-3.1-flash-lite` |
| (b) 中国内地后端 | `qwen3.7-plus`（阿里云百炼，北京） | `doubao-seed-2.1-lite`（火山方舟） | `qwen3.5-flash` / `doubao-seed-2.0-mini` |

- 架构：**模型只负责"识别 + 克数 + 烹饪方式"，营养素由后端查库计算**；模型自报的热量只用于交叉校验和置信度判定。微量营养素一律以数据库为准。
- 现实精度：当前最好的通用 VLM，在单图上的热量 MAPE 约 **20–40%**；分量（克数）是最大误差源，且份量越大越倾向低估。不要对用户宣称"精确"，要做克数可修正的 UI。
- 关键合规风险：Anthropic 禁止"由不受支持地区（含中国内地、香港）的个人或实体直接或间接多数控股或控制"的主体使用。若项目主体属于这一类，**不要把 Claude 作为生产依赖**。
- 端侧：只值得做两件事：上传前把图片缩到约 1 MP，以及用 Vision 做低成本的"是否为食物"预检。不要用端侧模型估营养。

---

## 1. 通用多模态 LLM 对比

### 1.1 计费假设

- 照片：上传前缩放到约 1280×960（约 1.23 MP）的 JPEG。
- 文本输入：system prompt + JSON schema，约 600 tokens。
- 输出：约 800 tokens JSON。关闭 thinking，或设为最低档（Gemini 的 thinking tokens 按输出价计费）。
- 汇率：1 USD ≈ 7.1 CNY（**TODO**：按当日汇率更新）。

各家每张图的 image tokens（按官方公式计算）：

| 厂商 | 公式 / 规则 | 约 1.23 MP 图片 |
| --- | --- | --- |
| Claude（4.7+ 高分辨率档） | 1000×1000 ≈ 1,296 tokens；长边上限 2,576 px，最多 4,784 tokens | 约 1,600 |
| OpenAI（gpt-5.4 / 5.5 / 6.x） | `ceil(w/32)×ceil(h/32)` 个 patch × 1.2 | 40×30×1.2 = 1,440 |
| Gemini 3.x | `media_resolution` 默认 `high` = 1,120；`medium` = 560；`low` = 280 | 1,120 |
| Qwen3.x / Qwen3-VL | `h×w/(32×32)+2`；默认 `max_pixels` 2,621,440 | 约 1,202 |
| Doubao seed-2.0+ | `detail=high`（默认）固定 1,280；`low` 为 1–1,280 | 1,280 |
| GLM-5V-Turbo | **TODO**（官方未查到公式），按约 1,500 估算 | 约 1,500 |

### 1.2 对比表

| 厂商 / 模型 ID | 定位 | 价格 / 1M tokens（in / out） | 单张成本（估） | 结构化输出 | API 数据政策 | 内地可用 | 海外可用 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Anthropic `claude-opus-5-5` | 旗舰 | $4 / $20 | $0.025 | `output_config.format` json_schema，GA | 默认 30 天内删除；可签 ZDR；训练政策见单独条款（**TODO**：确认 API 数据默认不用于训练） | 否（不支持地区） | 是；有中国控股主体限制 |
| `claude-sonnet-5-5` | 主力 | $2 / $10 | $0.012 | 同上 | 同上 | 否 | 是 |
| `claude-haiku-5-5` | 便宜快速 | $0.10 / $0.50（prompt ≤100K） | $0.0006 | 同上 | 同上 | 否 | 是 |
| OpenAI `gpt-6.2-sol` | 旗舰（性价比） | $2 / $10 | $0.012 | Structured Outputs `json_schema` strict | 默认不用于训练；abuse 日志最多保留 30 天；ZDR 需审批 | 否 | 是 |
| `gpt-6.1-terra` | 快速，官方定位含 visual reasoning | $1 / $5 | $0.006 | 同上（**TODO**：在 terra 上用图片 + strict schema 实测） | 同上 | 否 | 是 |
| `gpt-6-luna` | 最便宜 | $0.10 / $0.50 | $0.0006 | 同上（**TODO**：实测） | 同上 | 否 | 是 |
| Google `gemini-3.1-pro-preview` | 旗舰（preview） | $2 / $12 | $0.013 | `responseJsonSchema`（**TODO**：本次未能打开官方页面复核） | 付费档不用于改进产品；**免费档会用于改进产品** | 否 | 是 |
| `gemini-3.8-flash` | 主力 | $0.75 / $3.75（截至 2026-12-31）；**2027-01-01 起 $1.50 / $7.50** | $0.0043（2027 年起 $0.0086） | 同上 | 同上 | 否 | 是 |
| `gemini-3.5-flash-lite` | 便宜 | $0.30 / $2.50 | $0.0025 | 同上 | 同上 | 否 | 是 |
| `gemini-3.1-flash-lite` | 最便宜 | $0.25 / $1.50 | $0.0016 | 同上 | 同上 | 否 | 是 |
| 阿里云百炼 `qwen4-max` | 旗舰 | ¥10 / ¥30（北京） | ¥0.042 | json_schema 仅对**纯文本**生效；**带图片时静默降级为 `json_object`，schema 不强制** | 默认不用于训练（除非客户同意）；推理数据保留 ≤30 天；ZDR 仅国际站企业客户 | 是（北京） | 是（新加坡，约贵 10–90%） |
| `qwen3.7-plus` | 主力 | ¥2 / ¥8（0–256K，页面标注"限时8折"） | ¥0.010 | 同上 | 同上 | 是 | 是（新加坡 ¥3.0 / ¥12.0） |
| `qwen3.8-omni-flash` | 便宜全模态 | ¥0.8 / ¥2.7 | ¥0.0036 | `json_object`（**TODO**：实测） | 同上 | 是 | 是 |
| `qwen3.5-flash` | 最便宜 | ¥0.2 / ¥2（0–128K） | ¥0.002 | `json_object`，仅非 thinking 模式 | 同上 | 是 | 是 |
| `qwen3-vl-plus` | 上一代专用 VL | ¥1 / ¥10（0–32K） | ¥0.0098 | `json_object` | 同上 | 是 | 是 |
| 火山方舟 `doubao-seed-2.1-pro` | 旗舰 | ¥6 / ¥30 | ¥0.035 | json_schema strict（beta）；**图片 + schema 组合未见文档**（**TODO**） | **TODO**：未查到"推理数据不用于训练"的书面承诺；BytePlus 国际站条款：被安全过滤器触发的输入输出在马来西亚保留 180 天 | 是 | 通过 BytePlus ModelArk（**TODO**：可用模型与美元价格未查到） |
| `doubao-seed-2.1-lite` | 主力便宜 | ¥0.8 / ¥2.7 | ¥0.0037 | 同上 | 同上 | 是 | 同上 |
| `doubao-seed-2.0-mini` | 最便宜 | ¥0.2 / ¥2.0（0–32K） | ¥0.002 | 同上（**TODO**：确认是否支持图片输入） | 同上 | 是 | 同上 |
| 智谱 `glm-5v-turbo` | 视觉 + Agent / 编程（2026-04） | OpenRouter 标价 $1.20 / $4（**TODO**：bigmodel.cn 人民币官方价格未取到） | 约 $0.005 | 官方文档未写明 JSON mode（**TODO**） | **TODO** | 是 | 是（Z.ai / OpenRouter） |
| 百度 菜品识别（非 LLM） | 分类 API | 1 万次 ¥7；超过 100 万次/月 ¥0.3/千次 | 约 ¥0.0007 | 固定 JSON | **TODO** | 是 | **TODO** |

**延迟**：本次未取到可比的公开实测（Artificial Analysis 只给出 Max reasoning 档的 TTFT，不适用于本场景）。**TODO**：用自有 50 张样图实测 p50 / p95。经验上，非 thinking 模式、输出约 800 tokens 时，Flash / Lite 档通常在数秒内返回。这是推测，未经验证。

**另外两点：**

- Gemini 2.5 Flash / 2.5 Pro 已 deprecated，将于 **2026-12-01** 被路由到新模型。不要硬编码旧 ID。
- Gemini 3.8 Flash 将于 **2027-01-01 涨价一倍**，成本模型要按 2027 年的价格做。

### 1.3 可用性要点

- **从中国内地访问**：Anthropic、OpenAI、Gemini 的支持地区都**不含**中国内地和香港。内地服务器直连属于违规且不稳定，不考虑。
- **Anthropic 的控股主体条款**：即使服务器部署在海外，只要主体被不受支持地区的个人或实体多数控股或控制，也在限制范围内。
- **从海外访问国产模型**：Qwen 有新加坡节点（`ap-southeast-1`）。豆包要通过 BytePlus ModelArk 访问。GLM 可通过 Z.ai 访问。

---

## 2. 专用食物识别 API

| 厂商 | 能力 | 价格 | 中餐覆盖 | 维护状态 |
| --- | --- | --- | --- | --- |
| Passio Nutrition AI | 照片 → 食物 + 份量 + 营养；另有条码、营养查询 | 按 token 计费：Starter $99/月（含 1M）；Growth $599/月（含 5M，续购 $10/M）；Pro $2,999/月（含 100M，续购 $2.5/M）。中位数约 **5,857 tokens/张照片**，折合 **$0.015–0.15/张** | 官方未说明（**TODO**：用 demo 实测） | 活跃（底层为 LLM 平台 EasyAI） |
| LogMeal | 菜品识别 + 份量 + 营养 + 菜谱，每次识别 1 credit | 套餐价格未公开；试用 30 天或 200 次 | 未说明（**TODO**） | 活跃 |
| Calorie Mama（Azumio） | 单接口：JPEG 长边 ≤544 px → 菜品 + 份量 + 每 kg 营养；仅英文名 | 价格在 dev.caloriemama.ai，未公开（**TODO**） | 未说明 | 2026-09 重建站点，API 无变化 |
| FatSecret Platform | 图像识别和 NLP 是 Premier Free / Premier 的付费附加项（每 25,000 次一档） | Basic 免费（5,000 次/天，仅美国数据）；Premier 按市场数报价 | Premier 有 62+ 国家数据集、26 种语言；中国是否在内为 **TODO** | 活跃 |
| Nutritionix（现属 Syndigo） | 自然语言 / 条码 → 营养；Track API | 需联系销售；二手来源称约 $449/月（**TODO**） | 以美国为主 | 文档在 Syndigo，未见停服 |
| Edamam | Vision API 属于 Food Database API，基于 LLM 识别食材和用量 | Nutrition Analysis $29 / $299 每月；Vision 价格未列出（**TODO**） | 未说明 | 活跃 |
| 百度 AI 菜品识别 | 返回菜名、置信度、**热量**、百科；支持自建菜品库 | 免费 3,000 次；1 万次 ¥7；10 QPS | 中餐强（页面自述 9 千 / 5 万种，前后矛盾） | 页面在售 |

**结论**：海外专用 API 的价格比通用 VLM 高 1–2 个数量级，中餐覆盖都没有明确承诺，而且其中多家（Passio、Edamam Vision）底层本身就是 LLM。不建议作为主链路。百度菜品识别很便宜，但只给菜名和热量，没有克数和宏量营养素，只适合作为内地的辅助信号或兜底。

---

## 3. 精度证据

| 研究 | 数据 | 结果 |
| --- | --- | --- |
| DiningBench（ACL 2026，arXiv 2604.10425） | 营养子集 1,650 道菜、8,856 张图；全库约 69% 为中餐（美团图片） | 热量 MAPE：Gemini-3-Pro-Preview **19.99%**，Gemini-3-Flash-Preview 20.57%，GPT-5 25.14%，Qwen2.5-VL-72B 35.88%，GPT-4o 41.67%，Qwen3-VL-4B-Thinking 52.91%（最差）。**注意**：部分真值由 Gemini-3-Pro 估算后人工核对，结果可能偏向 Gemini |
| bioRxiv 2026-07（10 模型，Nutrition5k，3,229 张） | 俯拍单图 | Gemini 3.0 Flash 热量最好（CCC 0.767，MAE 80.7 kcal）。Gemini 3.1 Flash Lite 的 CCC 为 0.754，食材 Jaccard 最高（0.655），成本 $0.59/千张。热量 MAPE 范围 39.8–120.4%，重量 MAE 53.7–97.6 g。参测的还有 GPT-5 Mini、Claude Haiku 4.5、FatSecret API |
| Fridolfsson 等，Curr Dev Nutr 2025 | 52 张标准化照片 | 能量 MAPE：GPT-4o 35.8%，Claude 3.5 Sonnet 35.8%，Gemini 1.5 Pro 64.2%。**份量越大越低估**（能量斜率 −0.34 到 −0.45） |
| Coburn 等，arXiv 2507.07048（ACETADA） | 营养师核验 | 加入上下文元数据（地点类型、餐次、已知食物项）后，MAE 和 MAPE 都下降 |
| Mu 等，2025（DOI 10.1145/3765612.3767255） | Nutrition5k + 手机图片 | 加入 RGB-D 模型预测的重量后，Gemini 2.5 Flash 碳水 MAPE 从 56.6% 降到 39.5% |
| foodvision-bench（GitHub，2026-11） | mini-231，231 盘 | 宣称专用 App MAPE 为 10–16%，东亚菜系略差。**可信度低**：样本小，商业系统为人工辅助复现，而且榜首是发布方相关产品（**TODO**：核实利益关系） |

**可以预期的误差水平**：在真实的中式家常和混合餐盘上，最好的模型热量 MAPE 大约为 20–30%，中档模型约 30–45%。叠加用户修正克数后，可以降到 15% 以内（推测，**TODO**：用自建标注集验证）。

**已知失效模式：**

1. 2D → 3D 份量推断：没有尺度参照，大份系统性低估。
2. 隐形油脂、糖、勾芡、酱汁：中式炒菜的油量主导误差，图像不可见。
3. 参数化知识偏置：模型会套用常见版本的营养值（例如默认的"红烧肉"），忽略实际做法。
4. 遮挡和混合菜：盖饭、汤里的食材、多人共享的菜盘。
5. CoT 或 thinking 不一定提升营养估计，小模型甚至会变差，还可能陷入推理死循环。
6. 微量营养素：模型基本靠"编"，不能直接采信。

---

## 4. 营养数据库与架构模式

### 4.1 数据库

| 数据源 | 许可 | 获取方式 | 适用 |
| --- | --- | --- | --- |
| USDA FoodData Central | **CC0 公共领域**，建议注明来源 | API（data.gov key，1,000 次/小时/IP）+ 批量下载 | 食材级数据（Foundation、SR Legacy），FNDDS 有混合菜 |
| 中国食物成分表（营养与健康所编著，北京大学医学出版社出版） | 有版权的出版物；数据库商用授权条款**未查到（TODO：联系营养与健康所 / 出版社）** | 纸质书 / 官方查询平台；无公开 API（**TODO**） | 中国食材、地方食物，最贴合本项目 |
| Open Food Facts | 数据库 ODbL（share-alike），内容 DbCL，图片 CC-BY-SA | API（1 次调用 = 1 次真实扫码）+ 每日全量导出 | 包装食品条码；中国商品覆盖未知（**TODO**） |
| 台湾食品营养成分资料库 | 政府开放数据，免费 | 开放 API / 下载 | 中式食材的可替代开放来源 |

### 4.2 模式对比

| 模式 | 优点 | 缺点 |
| --- | --- | --- |
| A. 模型直接给出全部营养 | 实现最简单，混合菜有整体感 | 数值不可追溯、不稳定；微量营养素不可信；换模型后结果漂移 |
| B. 模型只出菜品 / 食材 + 克数，后端查库计算 | 可追溯、可审计、换模型时营养口径不变；微量营养素可靠 | 中式复合菜需要拆成食材 + 烹饪用油；需要维护食物映射表 |
| **C. 混合（推荐）** | 结合 A 和 B：B 给权威数值，A 用作校验 | 多一些实现量 |

**推荐流程（模式 C）：**

1. 端侧：缩放到约 1 MP 并压缩成 JPEG，可选做"是否为食物"预检（见第 5 节）。
2. VLM 用严格 schema 输出 `is_food`、`items[]`。每个 item 包含 `name_zh`、`name_en`、`cooking_method`、`ingredients[{name, grams}]`、`estimated_grams`、`visible_oil_level`、`confidence`，以及模型自估的 `kcal`、`protein_g`、`fat_g`、`carb_g`。
3. 后端映射：先对食材名做 embedding 或同义词检索，召回 Top-K 个数据库条目；再让便宜模型从候选中选择，只能输出候选 ID，避免幻觉。
4. 计算：`nutrient = grams × per_100g / 100`。按 `cooking_method` 加烹调油 / 糖的默认值（参数可配置，**TODO**：取值依据中国居民膳食指南或自建标注数据）。
5. 校验：DB 计算值与模型自估值偏差大于阈值（例如 35%）时标为低置信度，提示用户确认克数。
6. 用户修正克数和菜品后重算。修正数据保存下来，作为自有评测集和后续微调数据。
7. 存储原始图片引用、模型 ID 与版本、原始 JSON、DB 版本，保证可以重放计算。
8. 健康建议只基于第 4 步计算出的数值生成，可用便宜文本模型。

**结构化输出注意事项：**

- Qwen 带图请求不强制 schema，豆包图片 + schema 的组合未见文档。后端必须做 JSON Schema 校验，失败时重试一次再切换到 fallback。
- Claude 的 schema 不支持 `minimum` / `maximum`，数值范围要在后端校验。

---

## 5. 端侧方案

| 方案 | 结论 |
| --- | --- |
| 上传前缩放到约 1 MP 并压缩成 JPEG | **必须做**：节省带宽和 image tokens，对精度影响很小 |
| Apple Vision 图像分类（`ClassifyImageRequest`）做"是否为食物"预检 | **值得做**：离线、零成本，可拦截明显非食物的照片。**TODO**：确认分类体系中的食物相关标签与阈值。只做软提示，不硬拦截，误杀的代价高于一次 API 调用 |
| Apple Foundation Models（iOS 27 新增图片输入；端侧上下文 8,192 tokens） | **不值得作为依赖**：需要 Apple Intelligence 机型；中国内地上线时间未定（2026-12 计划上线，截至调研日未确认）；端侧小模型不适合估营养。最多用来做"是否为食物"或生成简短描述 |
| 自训 Core ML 分类器 | 现阶段不值得，维护成本高。服务端 VLM 本身就可以返回 `is_food=false`，成本约 $0.0005 |

---

## 6. 月成本估算（只含识别调用）

假设 1 张照片 = 1 次调用，在下表基础上加 15% 余量覆盖重试和 fallback。健康建议的文本调用另计；用 Flash-Lite、Luna 或 qwen3.5-flash 时，每条约 $0.0005，可忽略。

| 模型 | 单张 | 1k 张/天（3 万张/月） | 10k 张/天（30 万张/月） |
| --- | --- | --- | --- |
| `gemini-3.8-flash`（2026 价） | $0.0043 | $129 | $1,290 |
| `gemini-3.8-flash`（2027 价） | $0.0086 | $257 | $2,570 |
| `gemini-3.1-flash-lite` | $0.0016 | $49 | $489 |
| `gpt-6.1-terra` | $0.0061 | $182 | $1,815 |
| `gpt-6.2-sol` | $0.012 | $363 | $3,630 |
| `claude-haiku-5-5` | $0.0006 | $19 | $186 |
| `claude-sonnet-5-5` | $0.012 | $372 | $3,720 |
| `qwen3.7-plus`（北京） | ¥0.010 | ¥300（约 $42） | ¥3,000（约 $423） |
| `qwen3.5-flash` | ¥0.002 | ¥59 | ¥588 |
| `doubao-seed-2.1-lite` | ¥0.0037 | ¥110 | ¥1,098 |
| `doubao-seed-2.0-mini` | ¥0.002 | ¥59 | ¥594 |
| `glm-5v-turbo`（OpenRouter 价） | $0.0052 | $156 | $1,560 |
| Passio（Growth / Pro 套餐） | $0.015–0.08 | 约 $2,300 | 约 $7,100 |
| 百度 菜品识别 | ¥0.0007 | ¥21 | ¥210 |

---

## 7. 需要开通的账号

**海外后端：**

- Google AI Studio / Google Cloud 项目，并**开通 billing**（必须用付费档，免费档数据会用于改进产品）。
- OpenAI Platform（fallback）。
- data.gov API key（USDA FDC）。
- 可选：Anthropic Console。前提是主体不受"中国控股或控制"条款限制，否则不要开通。

**内地后端：**

- 阿里云账号 + 实名认证（企业认证更好）+ 开通百炼（华北2 北京）。
- 火山引擎账号 + 实名认证 + 开通方舟，并为所用模型创建接入点。
- 可选：百度智能云 / AI 开放平台（菜品识别兜底）。
- 合规：ICP 备案（内地服务器对外提供服务）；App Store 中国区的 App 备案；生成式 AI 服务相关的备案或登记义务（**TODO**：咨询法务，确认"调用已备案大模型 API 的应用"需要办理哪些手续）。
- 数据：中国食物成分表的商用授权（**TODO**：联系营养与健康所 / 北京大学医学出版社）。

---

## 8. 可执行的下一步

1. 建自有评测集：200–300 张中式家常和混合餐盘照片，称重并按食物成分表计算真值。这是所有选型的最终依据，现有公开 benchmark 都不能直接代表本场景。
2. 用同一个 prompt 和 schema 跑对比：`gemini-3.8-flash`、`gemini-3.1-flash-lite`、`gpt-6.1-terra`、`qwen3.7-plus`、`doubao-seed-2.1-lite`。指标为热量 MAPE、克数 MAPE、菜品识别 F1、p95 延迟、schema 失败率。
3. 在 Go 后端定义 provider 无关的 `Recognizer` 接口和统一 schema，按部署地区配置 primary / fallback。
4. 先用 USDA FDC 加小规模人工中餐映射表跑通模式 C，同时并行推进中国食物成分表的授权。

---

## 来源

- Claude 价格：https://platform.claude.com/docs/en/about-claude/pricing
- Claude Vision（image token）：https://platform.claude.com/docs/en/build-with-claude/vision
- Claude 结构化输出：https://platform.claude.com/docs/en/build-with-claude/structured-outputs
- Anthropic 数据保留：https://privacy.claude.com/en/articles/7996866-how-long-do-you-store-my-organization-s-data
- Anthropic 支持地区：https://www.anthropic.com/supported-countries
- OpenAI 价格：https://developers.openai.com/api/docs/pricing
- OpenAI 模型：https://developers.openai.com/api/docs/models
- OpenAI 图片 token：https://developers.openai.com/api/docs/guides/images-vision
- OpenAI 结构化输出：https://developers.openai.com/api/docs/guides/structured-outputs
- OpenAI 数据政策：https://developers.openai.com/api/docs/guides/your-data
- OpenAI 支持国家：https://developers.openai.com/api/docs/supported-countries
- Gemini 价格：https://aistudio.google.com/docs/pricing
- Gemini media resolution：https://aistudio.google.com/docs/media-resolution
- Gemini 可用地区：https://aistudio.google.com/docs/available-regions
- 百炼 视觉理解：https://help.aliyun.com/zh/model-studio/vision
- 百炼 模型列表：https://help.aliyun.com/zh/model-studio/models
- 百炼 价格：https://help.aliyun.com/zh/model-studio/model-pricing
- 百炼 JSON mode：https://help.aliyun.com/zh/model-studio/json-mode
- 百炼 数据保留：https://www.alibabacloud.com/help/en/model-studio/data-retention-policy
- 百炼 隐私说明：https://help.aliyun.com/en/model-studio/privacy-notice
- 火山方舟 价格：https://docs.volcengine.com/docs/82379/1544106
- 火山方舟 图片理解：https://docs.volcengine.com/docs/82379/1362931
- 火山方舟 结构化输出：https://www.volcengine.com/docs/82379/1568221
- 火山方舟 Responses API 结构化输出：https://www.volcengine.com/docs/82379/1958523
- BytePlus ModelArk 数据处理：https://docs.byteplus.com/zh-CN/docs/ModelArk/BytePlus_ModelArk_Data_Processing
- GLM-5V-Turbo 文档：https://docs.bigmodel.cn/cn/guide/models/vlm/glm-5v-turbo
- GLM-5V-Turbo（OpenRouter）：https://openrouter.ai/z-ai/glm-5v-turbo
- Passio 价格：https://www.passio.ai/pricing
- LogMeal 价格：https://logmeal.com/api/pricing/
- Calorie Mama API：https://www.caloriemama.ai/api
- FatSecret 版本对比：https://platform.fatsecret.com/api-editions
- Nutritionix API 指南（Syndigo）：https://docx.syndigo.com/developers/docs/nutritionix-api-guide
- Edamam：https://developer.edamam.com/edamam-nutrition-api
- 百度 菜品识别：https://ai.baidu.com/tech/imagerecognition/dish
- DiningBench：https://arxiv.org/html/2604.10425 ，https://aclanthology.org/2026.acl-long.1630.pdf
- 10 模型 benchmark（bioRxiv）：https://www.biorxiv.org/content/10.64898/2026.07.26.740845v1
- Fridolfsson 等 2025：https://pmc.ncbi.nlm.nih.gov/articles/PMC12513282/
- ACETADA / 元数据：https://arxiv.org/abs/2507.07048
- Mu 等 2025：https://digitalcommons.odu.edu/computerscience_fac_pubs/417
- 食物项核验与召回：https://arxiv.org/abs/2610.11144
- OmniFood8K：https://arxiv.org/pdf/2604.12356
- MM-Food-100K：https://arxiv.org/abs/2508.10429
- foodvision-bench：https://github.com/foodvision-bench/foodvision-bench
- USDA FDC API：https://fdc.nal.usda.gov/api-guide/
- Open Food Facts 数据：https://world.openfoodfacts.org/data
- 中国食物成分表（INFOODS）：https://www.fao.org/infoods/infoods/tables-and-databases/china/en/
- WWDC26 Foundation Models：https://developer.apple.com/videos/play/wwdc2026/241/
- Apple Intelligence 中国上线进展：https://www.macrumors.com/2026/07/15/apple-intelligence-cleared-to-launch-in-china/
