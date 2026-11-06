// 围住神经猫：用 Go 与 MyGo 原生 UI 复刻的经典小游戏。
//
// 点击空格放置路障，把小猫困在 9x9 的六边形棋盘上；小猫每回合沿最短路
// 朝边缘逃跑，走到棋盘边缘就轮到你输了。
package main

import (
	_ "embed"
	"fmt"
	"log"
	"math/rand/v2"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const (
	boardSize = 9                     // 9x9 棋盘
	diameter  = 40                    // 圆点直径
	space     = 5                     // 圆点间隙
	xOffset   = 31                    // 棋盘内水平偏移
	yOffset   = 210                   // 棋盘内垂直偏移
	catWidth  = 48                    // 小猫显示宽
	catHeight = 73.18                 // 小猫显示高
	frameTime = 38 * time.Millisecond // 精灵图换帧间隔

	spriteFrameW   = 61 // 精灵图单帧宽（像素）
	spriteFrameH   = 93 // 精灵图单帧高（像素）
	stayFrames     = 16 // 待机动画帧数
	encircleFrames = 15 // 被包围动画帧数

	windowWidth   = 512
	windowHeight  = 654
	contentX      = 12
	contentY      = 12
	contentWidth  = 488
	contentHeight = 630

	dialogWidth  = 467
	dialogHeight = 474
)

var (
	//go:embed assets/bg.jpg
	bgData []byte
	//go:embed assets/stay.png
	stayData []byte
	//go:embed assets/encircle.png
	encircleData []byte
	//go:embed assets/replay_main.png
	replayMainData []byte
	//go:embed assets/victory.png
	victoryData []byte
	//go:embed assets/failed.png
	failedData []byte
	//go:embed assets/exit.png
	exitData []byte
	//go:embed assets/replay.png
	replayData []byte
)

// art 是游戏用到的位图，启动时解码一次。
type art struct {
	bg, stay, encircle *ui.Bitmap
	replayMain         *ui.Bitmap
	victory, failed    *ui.Bitmap
	exit, replay       *ui.Bitmap
}

func mustBitmap(data []byte) *ui.Bitmap {
	b, err := ui.DecodeBitmap(data)
	if err != nil {
		log.Fatal(err)
	}
	return b
}

func loadArt() art {
	return art{
		bg:         mustBitmap(bgData),
		stay:       mustBitmap(stayData),
		encircle:   mustBitmap(encircleData),
		replayMain: mustBitmap(replayMainData),
		victory:    mustBitmap(victoryData),
		failed:     mustBitmap(failedData),
		exit:       mustBitmap(exitData),
		replay:     mustBitmap(replayData),
	}
}

type pos struct {
	row, col int
}

func (p pos) index() int { return p.row*boardSize + p.col }

func inBounds(p pos) bool {
	return p.row >= 0 && p.row < boardSize && p.col >= 0 && p.col < boardSize
}

func onEdge(p pos) bool {
	return p.row == 0 || p.col == 0 || p.row == boardSize-1 || p.col == boardSize-1
}

// neighbors 返回六边形相邻格：奇数行整体向右偏移半个圆。
func neighbors(p pos) [6]pos {
	odd := p.row % 2
	return [6]pos{
		{p.row - 1, p.col - 1 + odd}, {p.row - 1, p.col + odd},
		{p.row, p.col - 1}, {p.row, p.col + 1},
		{p.row + 1, p.col - 1 + odd}, {p.row + 1, p.col + odd},
	}
}

func cellX(row, col int) int {
	return (diameter+space)*col + xOffset + (diameter/2)*(row%2)
}

func cellY(row int) int { return diameter*row + yOffset }

// distanceToEdge 多源 BFS：以所有可用的边缘格为起点，算出每个空格到边缘
// 的最短步数；走不到边缘的格子为 -1。
func distanceToEdge(blocked *[boardSize * boardSize]bool) [boardSize * boardSize]int {
	var dist [boardSize * boardSize]int
	for i := range dist {
		dist[i] = -1
	}
	var queue []pos

	for r := 0; r < boardSize; r++ {
		for c := 0; c < boardSize; c++ {
			p := pos{r, c}
			if !onEdge(p) || blocked[p.index()] {
				continue
			}
			dist[p.index()] = 0
			queue = append(queue, p)
		}
	}

	for head := 0; head < len(queue); head++ {
		p := queue[head]
		for _, n := range neighbors(p) {
			if !inBounds(n) {
				continue
			}
			ni := n.index()
			if dist[ni] != -1 || blocked[ni] {
				continue
			}
			dist[ni] = dist[p.index()] + 1
			queue = append(queue, n)
		}
	}
	return dist
}

type game struct {
	art art

	blocked    [boardSize * boardSize]bool
	cat        pos
	steps      int
	over       bool
	enclosed   bool
	showResult bool
	won        bool

	resultInfo  string
	resultTitle string

	animStart time.Time // 当前动画段的起点
	animEnd   time.Time // 游戏结束时冻结的动画时间
}

func newGame() *game {
	g := &game{}
	g.reset()
	return g
}

func (g *game) reset() {
	for i := range g.blocked {
		g.blocked[i] = false
	}

	// 随机放置 6~12 个路障，避开正中心（小猫出生点）
	count := 6 + rand.IntN(7)
	for placed := 0; placed < count; {
		r, c := rand.IntN(boardSize), rand.IntN(boardSize)
		if r == boardSize/2 && c == boardSize/2 {
			continue
		}
		i := r*boardSize + c
		if g.blocked[i] {
			continue
		}
		g.blocked[i] = true
		placed++
	}

	g.cat = pos{boardSize / 2, boardSize / 2}
	g.steps = 0
	g.over = false
	g.enclosed = false
	g.showResult = false
	g.won = false
	g.resultInfo = ""
	g.resultTitle = ""
	g.animStart = time.Now()
	g.animEnd = time.Time{}
}

// click 在 (row, col) 放置路障，然后让小猫走一步。
func (g *game) click(row, col int) {
	if g.over || row < 0 || row >= boardSize || col < 0 || col >= boardSize {
		return
	}
	i := row*boardSize + col
	if g.blocked[i] {
		return
	}

	g.blocked[i] = true
	g.steps++

	if onEdge(g.cat) {
		g.finish(false)
		return
	}

	next, hasPath, ok := g.chooseStep()
	if !ok {
		g.finish(true) // 四周全被堵死
		return
	}

	g.cat = next

	if !hasPath && !g.enclosed {
		// 已经没有通往边缘的路了，换成"被包围"动画
		g.enclosed = true
		g.animStart = time.Now()
	}

	if onEdge(g.cat) {
		g.finish(false)
	}
}

// chooseStep 选下一步：向边缘推进的同时避免钻进死角；等好的走法之间随机
// 选，行为不机械。ok 为 false 表示小猫四周全被堵死（玩家获胜）。
func (g *game) chooseStep() (next pos, hasPath, ok bool) {
	var free []pos
	for _, n := range neighbors(g.cat) {
		if inBounds(n) && !g.blocked[n.index()] {
			free = append(free, n)
		}
	}
	if len(free) == 0 {
		return pos{}, false, false
	}

	dist := distanceToEdge(&g.blocked)
	hasPath = dist[g.cat.index()] >= 0

	if !hasPath {
		// 已经被围在一片区域里：在能走的空格中随机挪动
		return free[rand.IntN(len(free))], false, true
	}

	// 优先走向离边缘更近的格（通常是唯一方向，也可能有几个并列）
	bestDist := int(^uint(0) >> 1)
	for _, n := range free {
		d := dist[n.index()]
		if d >= 0 && d < bestDist {
			bestDist = d
		}
	}

	// 等距时看机动性：走完后周围空格越多，越不容易马上被围死
	bestMobility := -1
	var moves []pos
	for _, n := range free {
		if dist[n.index()] != bestDist {
			continue
		}
		mobility := 0
		for _, m := range neighbors(n) {
			if inBounds(m) && !g.blocked[m.index()] {
				mobility++
			}
		}
		if mobility > bestMobility {
			bestMobility = mobility
			moves = moves[:0]
		}
		if mobility == bestMobility {
			moves = append(moves, n)
		}
	}

	return moves[rand.IntN(len(moves))], true, true
}

func (g *game) finish(win bool) {
	g.over = true
	g.animEnd = time.Now()
	g.won = win
	g.showResult = true

	if !win {
		return
	}

	switch {
	case g.steps <= 5:
		g.resultTitle = "获得称号：精神病院长的帮手"
	case g.steps <= 10:
		g.resultTitle = "获得称号：隔壁的王伯伯"
	case g.steps <= 15:
		g.resultTitle = "获得称号：根本停不下来"
	case g.steps <= 20:
		g.resultTitle = "获得称号：喵了个咪的"
	default:
		g.resultTitle = "获得称号：小菜猫"
	}
	g.resultInfo = fmt.Sprintf("您用 %d 步抓住了神经猫", g.steps)
	g.enclosed = true
}

// animElapsed 返回当前这一轮动画已经播放的时间。
func (g *game) animElapsed(now time.Time) time.Duration {
	if g.over && !g.animEnd.IsZero() {
		now = g.animEnd
	}
	if now.Before(g.animStart) {
		return 0
	}
	return now.Sub(g.animStart)
}

func (g *game) catLeft() float32 {
	return float32(cellX(g.cat.row, g.cat.col)+diameter/2) - catWidth/2
}

func (g *game) catTop() float32 {
	return float32(cellY(g.cat.row)+diameter/2) - 73
}

// view 每一帧根据状态重建界面。
func (g *game) view(c *ui.Context) {
	ui.Box(c).Fill().Background(ui.Hex("#ececec")).Children(func() {
		g.board(c)
		if g.showResult {
			g.result(c)
		}
	})
}

func (g *game) board(c *ui.Context) {
	ui.Box(c).Absolute().Left(contentX).Top(contentY).Size(contentWidth, contentHeight).Clip().
		Draw(func(p *ui.Painter, r ui.Rect) {
			p.Image(g.art.bg, r, ui.FillBox)
		}).Children(func() {
		// 所有圆点
		for row := 0; row < boardSize; row++ {
			for col := 0; col < boardSize; col++ {
				i := row*boardSize + col
				bg := ui.Hex("#b5b5b5")
				if g.blocked[i] {
					bg = ui.Hex("#ff845e")
				}
				cell := ui.Box(c).
					Absolute().Left(float32(cellX(row, col))).Top(float32(cellY(row))).
					Size(diameter, diameter).Radius(diameter / 2).
					Background(bg).Cursor(ui.CursorPointer)
				if cell.Clicked() {
					g.click(row, col)
				}
			}
		}

		// 小猫：精灵图逐帧动画
		ui.Box(c).Absolute().Left(g.catLeft()).Top(g.catTop()).Size(catWidth, catHeight).
			PassThrough().Draw(func(p *ui.Painter, r ui.Rect) {
			sheet, frames := g.art.stay, stayFrames
			if g.enclosed {
				sheet, frames = g.art.encircle, encircleFrames
			}
			frame := int(g.animElapsed(p.Now())/frameTime) % frames
			src := ui.Rect{
				X: float32((frame % 4) * spriteFrameW),
				Y: float32((frame / 4) * spriteFrameH),
				W: spriteFrameW,
				H: spriteFrameH,
			}
			p.ImageRegion(sheet, src, r, ui.ImageOptions{})
			if !g.over {
				p.After(frameTime)
			}
		})

		// 重新开始按钮
		replay := ui.Box(c).Absolute().Left(181).Top(134).Size(33, 36).
			Cursor(ui.CursorPointer).Draw(func(p *ui.Painter, r ui.Rect) {
			p.Image(g.art.replayMain, r, ui.FillBox)
		})
		if replay.Clicked() {
			g.reset()
		}
	})
}

func (g *game) result(c *ui.Context) {
	ui.Box(c).Absolute().Left(0).Top(0).Size(windowWidth, windowHeight).
		Background(ui.RGBA(0, 0, 0, 0.4)).Children(func() {
		ui.Box(c).
			Absolute().Left(float32(windowWidth-dialogWidth)/2).Top((windowHeight-dialogHeight)/2).
			Size(dialogWidth, dialogHeight).Children(func() {
			ui.Box(c).Absolute().Left(0).Top(0).Size(dialogWidth, 354).PassThrough().
				Draw(func(p *ui.Painter, r ui.Rect) {
					pic := g.art.failed
					if g.won {
						pic = g.art.victory
					}
					p.Image(pic, r, ui.FillBox)
				})

			if g.won {
				g.resultText(c, g.resultInfo, 228)
				g.resultText(c, g.resultTitle, 272)
			}

			exit := ui.Box(c).Absolute().Left(12).Top(372).Size(186, 90).
				Cursor(ui.CursorPointer).Draw(func(p *ui.Painter, r ui.Rect) {
				p.Image(g.art.exit, r, ui.FillBox)
			})
			if exit.Clicked() {
				mygo.App.Quit()
			}

			replay := ui.Box(c).Absolute().Left(269).Top(372).Size(186, 90).
				Cursor(ui.CursorPointer).Draw(func(p *ui.Painter, r ui.Rect) {
				p.Image(g.art.replay, r, ui.FillBox)
			})
			if replay.Clicked() {
				g.reset()
			}
		})
	})
}

func (g *game) resultText(c *ui.Context, s string, top float32) {
	ui.Box(c).Absolute().Left(0).Top(top).Size(dialogWidth, 30).PassThrough().
		Draw(func(p *ui.Painter, r ui.Rect) {
			label := ui.Span{Text: s, Size: 17, Weight: 700, Color: ui.Hex("#d81e06")}
			w, h := p.MeasureText(0, label)
			p.RichText(r.X+(r.W-w)/2, r.Y+(r.H-h)/2, 0, label)
		})
}

func main() {
	g := newGame()
	g.art = loadArt()

	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:           "围住神经猫",
			Width:           windowWidth,
			Height:          windowHeight,
			DisableResize:   true,
			BackgroundColor: "#ececec",
			Content:         ui.View(g.view),
		})
	})

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
