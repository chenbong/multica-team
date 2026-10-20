## 1. 行内公式 `$...$`

质能方程 $E = mc^2$，欧拉恒等式 $e^{i\pi} + 1 = 0$，等差数列求和 $\sum_{k=1}^{n} k = \frac{n(n+1)}{2}$，正态分布 $X \sim \mathcal{N}(\mu, \sigma^2)$。

## 2. 行内公式 `\(...\)`

勾股定理 \(a^2 + b^2 = c^2\)，导数定义 \(f'(x) = \lim_{h \to 0} \frac{f(x+h) - f(x)}{h}\)。

## 3. 块级公式 `$$...$$`

$$
\int_{-\infty}^{+\infty} e^{-x^2} \, \mathrm{d}x = \sqrt{\pi}
$$

## 4. 块级公式 `\[...\]`

\[
\zeta(s) = \sum_{n=1}^{\infty} \frac{1}{n^s} = \prod_{p\ \text{prime}} \frac{1}{1 - p^{-s}}
\]

## 5. math 代码块（GitHub / GitLab 风格）

```math
\oint_{\partial \Sigma} \mathbf{B} \cdot \mathrm{d}\boldsymbol{\ell} = \mu_0 \iint_{\Sigma} \mathbf{J} \cdot \mathrm{d}\mathbf{S}
```

## 6. 分式、根式、上下标

$$
\frac{a}{b}, \quad \dfrac{1}{1 + \dfrac{1}{x}}, \quad \sqrt{x^2 + y^2}, \quad \sqrt[3]{27} = 3, \quad x_i^2, \quad a_{ij}^{(k)}, \quad e^{-\frac{x^2}{2\sigma^2}}
$$

连分数：

$$
\phi = 1 + \cfrac{1}{1 + \cfrac{1}{1 + \cfrac{1}{1 + \cdots}}}
$$

二项式系数（行内）：$\binom{n}{k} = \frac{n!}{k!\,(n-k)!}$

## 7. 求和、积分、极限、乘积

$$
\sum_{i=1}^{n} x_i, \quad \prod_{i=1}^{n} x_i, \quad \lim_{n \to \infty} \left(1 + \frac{1}{n}\right)^n = e, \quad \int_a^b f(x)\,\mathrm{d}x, \quad \iint_D f(x,y)\,\mathrm{d}A, \quad \oint_C \mathbf{F} \cdot \mathrm{d}\mathbf{r}
$$

多行下标：

$$
\sum_{\substack{0 \le i \le m \\ 0 < j < n}} P(i, j)
$$

## 8. 希腊字母与常用符号

小写：$\alpha\ \beta\ \gamma\ \delta\ \epsilon\ \varepsilon\ \zeta\ \eta\ \theta\ \lambda\ \mu\ \pi\ \rho\ \sigma\ \tau\ \phi\ \varphi\ \chi\ \psi\ \omega$

大写：$\Gamma\ \Delta\ \Theta\ \Lambda\ \Xi\ \Pi\ \Sigma\ \Phi\ \Psi\ \Omega$

运算与关系：$\pm\ \times\ \div\ \cdot\ \le\ \ge\ \ne\ \approx\ \equiv\ \propto\ \sim\ \ll\ \gg$

集合与逻辑：$\in\ \notin\ \subset\ \subseteq\ \cup\ \cap\ \emptyset\ \forall\ \exists\ \neg\ \land\ \lor\ \Rightarrow\ \Leftrightarrow$

箭头与其他：$\to\ \leftarrow\ \mapsto\ \uparrow\ \infty\ \nabla\ \partial\ \hbar\ \ell\ \aleph_0$

## 9. 字体样式

$\mathbb{R}\ \mathbb{N}\ \mathbb{Z}\ \mathbb{Q}\ \mathbb{C}$，$\mathcal{L}\ \mathcal{O}\ \mathcal{F}$，$\mathscr{H}$，$\mathfrak{g}$，$\mathbf{v}$，$\boldsymbol{\theta}$，$\mathrm{d}x$，$\mathit{text}$，$\mathsf{T}$，$\mathtt{code}$，$\text{公式里的中文}$

## 10. 括号与定界符

$$
\left( \frac{a}{b} \right), \quad \left[ \sum_{i} x_i \right], \quad \left\{ x \mid x > 0 \right\}, \quad \langle u, v \rangle, \quad \lfloor x \rfloor, \quad \lceil x \rceil, \quad |x|, \quad \|\mathbf{x}\|_2, \quad \left. \frac{\mathrm{d}f}{\mathrm{d}x} \right|_{x=0}
$$

手动尺寸：$\Bigg( \bigg( \Big( \big( x \big) \Big) \bigg) \Bigg)$

## 11. 矩阵

$$
\begin{matrix} a & b \\ c & d \end{matrix} \quad
\begin{pmatrix} a & b \\ c & d \end{pmatrix} \quad
\begin{bmatrix} a & b \\ c & d \end{bmatrix} \quad
\begin{Bmatrix} a & b \\ c & d \end{Bmatrix} \quad
\begin{vmatrix} a & b \\ c & d \end{vmatrix} \quad
\begin{Vmatrix} a & b \\ c & d \end{Vmatrix}
$$

一般矩阵：

$$
A = \begin{pmatrix}
a_{11} & a_{12} & \cdots & a_{1n} \\
a_{21} & a_{22} & \cdots & a_{2n} \\
\vdots & \vdots & \ddots & \vdots \\
a_{m1} & a_{m2} & \cdots & a_{mn}
\end{pmatrix}
$$

增广矩阵（`array` 带竖线）：

$$
\left[\begin{array}{ccc|c}
1 & 2 & 3 & 4 \\
0 & 1 & 5 & 6 \\
0 & 0 & 1 & 7
\end{array}\right]
$$

## 12. 多行对齐与分段函数

`aligned` 推导：

$$
\begin{aligned}
(a + b)^2 &= (a + b)(a + b) \\
&= a^2 + ab + ba + b^2 \\
&= a^2 + 2ab + b^2
\end{aligned}
$$

`cases` 分段函数：

$$
|x| = \begin{cases}
x, & x \ge 0 \\
-x, & x < 0
\end{cases}
$$

方程组：

$$
\left\{
\begin{aligned}
2x + 3y &= 7 \\
x - y &= 1
\end{aligned}
\right.
$$

`gathered` 居中多行：

$$
\begin{gathered}
a = b + c \\
d + e = f + g + h
\end{gathered}
$$

## 13. 修饰符与标注

$\hat{x}\ \bar{x}\ \tilde{x}\ \vec{v}\ \dot{x}\ \ddot{x}\ \overline{AB}\ \underline{xy}\ \overrightarrow{AB}\ \widehat{xyz}\ \widetilde{abc}$

$$
\underbrace{1 + 2 + \cdots + n}_{n\ \text{项}} = \overbrace{\frac{n(n+1)}{2}}^{\text{求和公式}}
$$

$$
A \xrightarrow{\text{条件}} B, \quad x \stackrel{\text{def}}{=} y, \quad a \overset{?}{=} b
$$

## 14. 编号、框选、颜色

$$
E = mc^2 \tag{1}
$$

$$
\boxed{e^{i\theta} = \cos\theta + i\sin\theta}
$$

$$
{\color{red} x^2} + {\color{blue} 2xy} + {\color{green} y^2} = \textcolor{purple}{(x + y)^2}
$$

## 15. 化学式（`\ce`，依赖 mhchem 扩展，很多渲染器默认没开）

$$
\ce{2H2 + O2 -> 2H2O}, \quad \ce{CO2 + H2O <=> H2CO3}
$$

## 16. 综合示例

麦克斯韦方程组：

$$
\begin{aligned}
\nabla \cdot \mathbf{E} &= \frac{\rho}{\varepsilon_0} \\
\nabla \cdot \mathbf{B} &= 0 \\
\nabla \times \mathbf{E} &= -\frac{\partial \mathbf{B}}{\partial t} \\
\nabla \times \mathbf{B} &= \mu_0 \mathbf{J} + \mu_0 \varepsilon_0 \frac{\partial \mathbf{E}}{\partial t}
\end{aligned}
$$

薛定谔方程：

$$
i\hbar \frac{\partial}{\partial t} \Psi(\mathbf{r}, t) = \left[ -\frac{\hbar^2}{2m} \nabla^2 + V(\mathbf{r}, t) \right] \Psi(\mathbf{r}, t)
$$

傅里叶变换：

$$
\hat{f}(\xi) = \int_{-\infty}^{\infty} f(x)\, e^{-2\pi i x \xi} \,\mathrm{d}x
$$

贝叶斯公式：

$$
P(A \mid B) = \frac{P(B \mid A)\, P(A)}{\sum_{i} P(B \mid A_i)\, P(A_i)}
$$

Attention 与交叉熵损失：

$$
\operatorname{Attention}(Q, K, V) = \operatorname{softmax}\left( \frac{QK^\top}{\sqrt{d_k}} \right) V, \qquad \mathcal{L} = -\frac{1}{N} \sum_{i=1}^{N} \sum_{c=1}^{C} y_{i,c} \log \hat{y}_{i,c}
$$

对称 INT-$b$ 量化与反量化：

$$
s = \frac{\max \lvert W \rvert}{2^{b-1} - 1}, \qquad W_q = \operatorname{clamp}\left( \operatorname{round}\left( \frac{W}{s} \right),\ -2^{b-1},\ 2^{b-1} - 1 \right), \qquad \hat{W} = s \cdot W_q
$$

## 17. 表格中的行内公式

| 名称 | 公式 |
|---|---|
| 均值 | $\mu = \frac{1}{n}\sum_{i=1}^{n} x_i$ |
| 方差 | $\sigma^2 = \frac{1}{n}\sum_{i=1}^{n} (x_i - \mu)^2$ |
| $p$-范数 | $\lVert \mathbf{x} \rVert_p = \left( \sum_i \lvert x_i \rvert^p \right)^{1/p}$ |
| Softmax | $\operatorname{softmax}(z)_i = \frac{e^{z_i}}{\sum_j e^{z_j}}$ |

表格里的公式用的是 `\lvert` / `\lVert`，没有直接写 `|`，因为 Markdown 表格会把 `|` 当成列分隔符。

## 18. 边界情况

- 美元符号转义：这件商品售价 \$100，另一件 \$200，这两处不应被识别成公式。
- 公式紧贴中文：设$x>0$，则$\sqrt{x}$有意义（部分渲染器要求 `$` 两侧有空格）。
- 含下划线：$a_1 + a_2 + \cdots + a_n$ 和 $b_1$ 之间的文字不应变成斜体。
- 含星号：$a * b * c$ 不应被解析成加粗或斜体。
