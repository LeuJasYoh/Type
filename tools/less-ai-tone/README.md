# less-ai-tone（内置副本）

本目录是「Less AI Tone」的规则文档与检测脚本副本，供本仓库写、改对外文字时使用。
上游为 MIT 授权，版权归 shiujan（见 [LICENSE](LICENSE)）。副本只取规则与脚本，
上游的宣传图与英文版未收录，因此这里另写一份说明而不是沿用上游 README。

## 怎么用

改 README、`release-notes/`、界面文案之前先读 [SKILL.md](SKILL.md)，它按优先级列出
11 条改写规则，每条都带可定位的触发标记。改完复量：

```bash
python tools/less-ai-tone/scripts/check-translationese.py README.md release-notes/
```

脚本输出每千字的标记频率，拿人类基准对照就能看出哪一项超了。本仓库 2026-09 清理时
的读数：破折号 README 6.87、AGENTS.md 5.22，人类基准 0.80、AI 均值 2.38，是唯一
超标的项；其余标记均为 0 或落在豁免范围。

另外两个脚本需要人类与 AI 两侧语料才能对比，本仓库没有那样的语料，收在这里只为
保持副本完整：

- `scripts/check-structure.py` 段级结构特征（相邻句同构、段首零主语）
- `scripts/compare-human-ai.py` 两侧语料的指标对照

## 两条最容易用错的地方

SKILL.md 是白名单式的成稿清理规则，不是写作指南：只有命中规则的句子才改，改动
限于解决该问题所必需的范围，未命中的逐字保留。「句长、顿号罗列、设问、比喻、被动句、
名词化」被明确列为不作为改写理由，照着感觉"改得更像人写的"会把这些正常写法一起改坏。

实测数据与各条规则的收录依据在 [RESEARCH.md](RESEARCH.md)，包括若干被推翻的预设
（例如"AI 爱用被动句""AI 爱拆短段"都不成立）。有疑问时先查这份，别按印象下判断。
