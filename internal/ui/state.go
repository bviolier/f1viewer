package ui

import (
	"fmt"
	"time"

	"github.com/SoMuchForSubtlety/f1viewer/internal/cmd"
	"github.com/SoMuchForSubtlety/f1viewer/internal/config"
	"github.com/SoMuchForSubtlety/f1viewer/internal/github"
	"github.com/SoMuchForSubtlety/f1viewer/internal/secret"
	"github.com/SoMuchForSubtlety/f1viewer/internal/util"
	f1tvV1 "github.com/SoMuchForSubtlety/f1viewer/pkg/f1tv/v1"
	f1tvV2 "github.com/SoMuchForSubtlety/f1viewer/pkg/f1tv/v2"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// TODO: rework
var activeTheme = struct {
	CategoryNodeColor   tcell.Color
	FolderNodeColor     tcell.Color
	ItemNodeColor       tcell.Color
	ActionNodeColor     tcell.Color
	LoadingColor        tcell.Color
	LiveColor           tcell.Color
	MultiCommandColor   tcell.Color
	UpdateColor         tcell.Color
	NoContentColor      tcell.Color
	InfoColor           tcell.Color
	ErrorColor          tcell.Color
	TerminalAccentColor tcell.Color
	TerminalTextColor   tcell.Color
}{
	CategoryNodeColor:   tcell.ColorOrange,
	FolderNodeColor:     tcell.ColorWhite,
	ItemNodeColor:       tcell.ColorLightGreen,
	ActionNodeColor:     tcell.ColorDarkCyan,
	LoadingColor:        tcell.ColorDarkCyan,
	LiveColor:           tcell.ColorRed,
	MultiCommandColor:   tcell.ColorAquaMarine,
	UpdateColor:         tcell.ColorDarkRed,
	NoContentColor:      tcell.ColorOrangeRed,
	InfoColor:           tcell.ColorGreen,
	ErrorColor:          tcell.ColorRed,
	TerminalAccentColor: tcell.ColorGreen,
	TerminalTextColor:   tview.Styles.PrimaryTextColor,
}

type UIState struct {
	version string
	cfg     config.Config

	app *tview.Application

	textWindow *tview.TextView
	treeView   *tview.TreeView

	logger util.Logger

	// TODO: replace activeTheme
	// theme config.Theme

	changes chan *tview.TreeNode

	v2     *f1tvV2.F1TV
	v1     *f1tvV1.F1TV
	secret *secret.SecretStore
	cmd    *cmd.Store
}

func NewUI(cfg config.Config, version string) *UIState {
	ui := UIState{
		version: version,
		cfg:     cfg,
		changes: make(chan *tview.TreeNode),
		secret:  &secret.SecretStore{},
		v2:      f1tvV2.NewF1TV(version),
		v1:      f1tvV1.NewF1TV(version),
	}
	ui.applyTheme(cfg.Theme)

	ui.app = tview.NewApplication()
	ui.app.EnableMouse(true)

	root := tview.NewTreeNode("Categories").SetSelectable(false)

	ui.treeView = tview.NewTreeView().
		SetRoot(root).
		SetCurrentNode(root).
		SetTopLevel(1)

	// refresh supported nodes on 'r' key press or quit on 'q'
	ui.treeView.SetInputCapture(ui.TreeInputHanlder)

	ui.textWindow = tview.NewTextView().
		SetWordWrap(false).
		SetWrap(cfg.TerminalWrap).
		SetDynamicColors(true).
		SetChangedFunc(func() { ui.app.Draw() })
	ui.textWindow.SetBorder(true)

	ui.treeView.SetSelectedFunc(ui.toggleVisibility)

	ui.logger = ui.Logger()

	ui.cmd = cmd.NewStore(cfg.CustomPlaybackOptions, cfg.MultiCommand, cfg.Lang, ui.logger, activeTheme.TerminalAccentColor)

	err := ui.loginWithStoredCredentials()
	if err != nil {
		ui.initUIWithForm()
	} else {
		ui.logger.Info("logged in!")
		ui.initUI()
	}

	appendNodes(root, ui.getHomepageNodes()...)
	root.AddChild(ui.getLegacyContent())

	return &ui
}

func (ui *UIState) Stop() {
	ui.app.Stop()
}

func (ui *UIState) Run() error {
	done := make(chan error)
	go func() {
		done <- ui.app.Run()
	}()

	go ui.handleEvents()
	go ui.checkLive()
	go ui.loadUpdate()

	logOutNode := tview.NewTreeNode("Log Out").
		SetReference(&NodeMetadata{nodeType: ActionNode}).
		SetColor(activeTheme.ActionNodeColor)
	logOutNode.SetSelectedFunc(ui.logout)

	ui.treeView.GetRoot().AddChild(logOutNode)

	return <-done
}

func (s *UIState) logout() {
	err := s.secret.RemoveCredentials()
	if err != nil {
		s.logger.Error(err)
	}
	s.initUIWithForm()
}

func (s *UIState) loginWithStoredCredentials() error {
	username, password, token, err := s.secret.LoadCredentials()
	if err != nil {
		return err
	}
	return s.login(username, password, token)
}

func (s *UIState) login(username, pw, token string) error {
	// todo save auth token in credential store
	err := s.v1.Login(username, pw, token)
	if err != nil {
		return err
	}
	err = s.v2.Authenticate(username, pw)
	return err
}

func (s *UIState) initUIWithForm() {
	username, pw, _, _ := s.secret.LoadCredentials()
	form := tview.NewForm().
		AddInputField("email", username, 30, nil, func(text string) { username = text }).
		AddPasswordField("password", "", 30, '*', func(text string) { pw = text }).
		AddButton("test", func() {
			err := s.login(username, pw, "")
			if err == nil {
				s.logger.Info("credentials accepted")
			} else {
				s.logger.Error(err)
			}
		}).
		AddButton("save", func() { s.closeForm(username, pw) })

	formTreeFlex := tview.NewFlex()
	if !s.cfg.HorizontalLayout {
		formTreeFlex.SetDirection(tview.FlexRow)
	}

	if s.cfg.HorizontalLayout {
		formTreeFlex.
			AddItem(form, 50, 0, true).
			AddItem(s.treeView, 0, 1, false)
	} else {
		formTreeFlex.
			AddItem(form, 7, 0, true).
			AddItem(s.treeView, 0, 1, false)
	}

	masterFlex := tview.NewFlex()
	if s.cfg.HorizontalLayout {
		masterFlex.SetDirection(tview.FlexRow)
	}

	masterFlex.
		AddItem(formTreeFlex, 0, s.cfg.TreeRatio, true).
		AddItem(s.textWindow, 0, s.cfg.OutputRatio, false)

	s.app.SetRoot(masterFlex, true)
}

func (s *UIState) initUI() {
	flex := tview.NewFlex().
		AddItem(s.treeView, 0, s.cfg.TreeRatio, true).
		AddItem(s.textWindow, 0, s.cfg.OutputRatio, false)

	if s.cfg.HorizontalLayout {
		flex.SetDirection(tview.FlexRow)
	}

	s.app.SetRoot(flex, true)
}

func (s *UIState) closeForm(username, pw string) {
	err := s.login(username, pw, "")
	if err != nil {
		s.logger.Error(err)
	} else {
		err = s.secret.SaveCredentials(username, pw, "")
		if err != nil {
			s.logger.Error(err)
		}
	}
	s.initUI()
}

func (s *UIState) withBlink(node *tview.TreeNode, fn func(), after func()) func() {
	return func() {
		done := make(chan struct{})
		go func() {
			fn()
			done <- struct{}{}
		}()
		go func() {
			s.blinkNode(node, done)
			if after != nil {
				after()
			}
		}()
	}
}

func (s *UIState) blinkNode(node *tview.TreeNode, done chan struct{}) {
	originalText := node.GetText()
	originalColor := node.GetColor()
	color1 := originalColor
	color2 := activeTheme.LoadingColor
	node.SetText("loading...")

	ticker := time.NewTicker(200 * time.Millisecond)
	for {
		select {
		case <-done:
			node.SetText(originalText)
			node.SetColor(originalColor)
			s.app.Draw()
			return
		case <-ticker.C:
			node.SetColor(color2)
			s.app.Draw()
			c := color1
			color1 = color2
			color2 = c
		}
	}
}

func (ui *UIState) handleEvents() {
	for node := range ui.changes {
		insertNodeAtTop(ui.treeView.GetRoot(), node)
		ui.app.Draw()
	}
}

func (ui *UIState) loadUpdate() {
	release, new, err := github.CheckUpdate(ui.version)
	if err != nil {
		ui.logger.Error("failed to check for update: ", err)
	}
	if !new {
		return
	}

	ui.logger.Info("New version found!")
	ui.logger.Info(release.TagName)
	fmt.Fprintln(ui.logger, "\n[blue::bu]"+release.Name+"[-::-]")
	fmt.Fprintln(ui.logger, release.Body+"\n")

	updateNode := tview.NewTreeNode("UPDATE AVAILABLE").
		SetColor(activeTheme.UpdateColor).
		SetExpanded(false)
	getUpdateNode := tview.NewTreeNode("download update").
		SetColor(activeTheme.ActionNodeColor).
		SetSelectedFunc(func() {
			err := util.OpenBrowser("https://github.com/SoMuchForSubtlety/F1viewer/releases/latest")
			if err != nil {
				ui.logger.Error(err)
			}
		})

	appendNodes(updateNode, getUpdateNode)
	ui.changes <- updateNode
}
