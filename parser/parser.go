package parser

import (
	"github.com/patrickhuber/go-earley/chart"
	"github.com/patrickhuber/go-earley/grammar"
	"github.com/patrickhuber/go-earley/state"
	"github.com/patrickhuber/go-earley/token"
)

type Parser interface {
	Expected() []grammar.LexerRule
	Accepted() bool
	Location() int
	Pulse(tok token.Token) (bool, error)
}

type parser struct {
	location    int
	dottedRules grammar.RuleRegistry
	grammar     *grammar.Grammar
	chart       *chart.Chart
}

func New(g *grammar.Grammar) Parser {
	p := &parser{
		grammar:     g,
		chart:       chart.New(),
		dottedRules: grammar.NewRegistry(),
	}
	p.compute(g)
	p.initialize()
	return p
}

func (par *parser) compute(g *grammar.Grammar) {
	for p := range g.Productions {
		production := g.Productions[p]

		// this needs to be len(rhs)+1 because dots are between characters
		for i := 0; i <= len(production.RightHandSide); i++ {
			dr := &grammar.DottedRule{
				Production: production,
				Position:   i,
			}
			par.dottedRules.Register(dr)
		}
	}
}

func (p *parser) initialize() {
	p.location = 0
	p.chart = chart.New()
	start := p.grammar.StartProductions()

	for s := 0; s < len(start); s += 1 {
		production := start[s]
		state := p.NewState(production, 0, 0)
		p.chart.Enqueue(0, state)
	}
	p.reductionPass(p.location)
}

func (p *parser) NewState(production *grammar.Production, position int, origin int) state.State {
	dr, ok := p.dottedRules.Get(production, position)
	if !ok {
		panic("invalid state")
	}
	return p.chart.GetOrCreate(origin, state.NormalType, dr, origin)
}

func (p *parser) Pulse(tok token.Token) (bool, error) {
	p.scanPass(p.Location(), tok)

	tokenRecognized := len(p.chart.Sets) > p.Location()+1
	if !tokenRecognized {
		return false, nil
	}

	p.location++
	p.reductionPass(p.location)

	return true, nil
}

func (p *parser) scanPass(location int, tok token.Token) {
	set := p.chart.Sets[location]
	for _, s := range set.Scans {
		p.scan(s, location, tok)
	}
}

func (p *parser) scan(s *state.Normal, j int, tok token.Token) {

	sym := s.DottedRule.PostDotSymbol()

	// process lexer rules
	lexRule, ok := sym.(grammar.LexerRule)
	if !ok {
		return
	}

	// skip scanning if the token type doesn't match
	if lexRule.Type() != tok.Type() {
		return
	}

	// grab the next dotted rule from the registry
	rule, ok := p.dottedRules.Next(s.DottedRule)
	if !ok {
		return
	}

	i := s.Origin
	if p.chart.Contains(j+1, state.NormalType, rule, i) {
		return
	}

	// create a next from the dotted rule
	next := p.NewState(rule.Production, rule.Position, s.Origin)
	p.chart.Enqueue(j+1, next)
}

func (parser *parser) reductionPass(location int) {
	set := parser.chart.Sets[location]
	resume := true

	p := 0
	c := 0

	for resume {
		if c < len(set.Completions) {
			completion := set.Completions[c]
			parser.complete(completion, location)
			c++
			continue
		} else if p < len(set.Predictions) {
			evidence := set.Predictions[p]
			parser.predict(evidence, location)
			p++
			continue
		}
		resume = false
	}
}

func (p *parser) complete(completed *state.Normal, location int) {
	set := p.chart.Sets[completed.Origin]
	sym := completed.DottedRule.Production.LeftHandSide

	trans, ok := set.FindTransition(sym)
	if ok {
		p.leoComplete(trans, completed, location)
	} else {
		p.earleyComplete(completed, location)
	}
}

func (p *parser) leoComplete(trans *state.Transition, completed *state.Normal, location int) {
	dr := trans.DottedRule
	origin := trans.Origin

	// don't create memory for something that already exists
	if p.chart.Contains(location, state.NormalType, dr, origin) {
		return
	}

	// use the cache item to create the state instead of expanding out all the completed states
	topMostItem := p.NewState(dr.Production, dr.Position, origin)
	p.chart.Enqueue(location, topMostItem)
}

func (par *parser) earleyComplete(completed *state.Normal, location int) {

	// get the origin set for the completed state
	completedOrigin := completed.Origin
	set := par.chart.Sets[completedOrigin]

	sources := set.FindSourceStates(completed.DottedRule.Production.LeftHandSide)
	count := len(sources)

	for p := 0; p < count; p++ {
		prediction := sources[p]
		rule, ok := par.dottedRules.Next(prediction.DottedRule)
		if !ok {
			continue
		}
		origin := prediction.Origin

		if par.chart.Contains(location, state.NormalType, rule, origin) {
			continue
		}

		state := par.NewState(rule.Production, rule.Position, origin)
		par.chart.Enqueue(location, state)
	}
}

func (par *parser) predict(evidence *state.Normal, location int) {
	rule := evidence.DottedRule
	nonTerminal := rule.PostDotSymbol().(grammar.NonTerminal)
	productions := par.grammar.RulesFor(nonTerminal)

	count := len(productions)
	for p := 0; p < count; p++ {
		production := productions[p]
		par.predictProduction(location, production)
	}

	isNullable := false // par.grammar.IsTransativeNullable(nonTerminal)
	if isNullable {
		par.predictAycockHorspool(evidence, location)
	}
}

func (p *parser) predictProduction(location int, production *grammar.Production) {
	rule, ok := p.dottedRules.Get(production, 0)
	if !ok {
		return
	}
	if p.chart.Contains(location, state.NormalType, rule, location) {
		return
	}
	s := p.NewState(rule.Production, rule.Position, location)
	p.chart.Enqueue(location, s)
}

func (p *parser) predictAycockHorspool(evidence *state.Normal, location int) {
	panic("not implemented")
}

func (p *parser) Location() int {
	return p.location
}

// Accepted implements Parser.
func (p *parser) Accepted() bool {
	_, ok := p.findAcceptedCompletion(p.location)
	return ok
}

func (p *parser) findAcceptedCompletion(location int) (*state.Normal, bool) {
	set := p.chart.Sets[p.location]
	start := p.grammar.Start
	reductions := set.FindReductions(start)
	for c := 0; c < len(reductions); c++ {
		completion := reductions[c]
		if completion.Origin == 0 && completion.DottedRule.Production.LeftHandSide == start {
			return completion, true
		}
	}
	return nil, false
}

// Expected implements Parser.
func (p *parser) Expected() []grammar.LexerRule {
	set := p.chart.Sets[p.location]

	var expected []grammar.LexerRule
	for _, s := range set.Scans {
		postDot := s.DottedRule.PostDotSymbol()
		if postDot == nil {
			continue
		}
		lexRule, ok := postDot.(grammar.LexerRule)
		if !ok {
			continue
		}
		expected = append(expected, lexRule)
	}
	return expected
}
