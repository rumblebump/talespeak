package script

import (
	"fmt"

	"github.com/yuin/gopher-lua/ast"
)

// rewriteConcat turns every a .. b .. c in a parsed script into
// __concat(a, b, c) before it is compiled, so a script never runs
// gopher-lua's OP_CONCAT. That opcode joins up to 250 values in one Go call
// that neither the time limit nor the memory watcher can stop; __concat
// checks the result size first.
//
// Scripts may not use the name __concat themselves. A script that still
// replaces it (through _G) only changes its own .., and every string it
// could build that way comes from size-checked functions.
func rewriteConcat(chunk []ast.Stmt) error {
	w := &rewriter{}
	w.stmts(chunk)
	return w.err
}

type rewriter struct{ err error }

func (w *rewriter) reserved(line int, name string) {
	if name == concatName && w.err == nil {
		w.err = fmt.Errorf("line %d: the name %s is reserved", line, concatName)
	}
}

func (w *rewriter) names(line int, names []string) {
	for _, n := range names {
		w.reserved(line, n)
	}
}

func (w *rewriter) stmts(list []ast.Stmt) {
	for _, s := range list {
		w.stmt(s)
	}
}

func (w *rewriter) exprs(list []ast.Expr) {
	for i := range list {
		list[i] = w.expr(list[i])
	}
}

func (w *rewriter) stmt(s ast.Stmt) {
	switch s := s.(type) {
	case *ast.AssignStmt:
		w.exprs(s.Lhs)
		w.exprs(s.Rhs)
	case *ast.LocalAssignStmt:
		w.names(s.Line(), s.Names)
		w.exprs(s.Exprs)
	case *ast.FuncCallStmt:
		s.Expr = w.expr(s.Expr)
	case *ast.DoBlockStmt:
		w.stmts(s.Stmts)
	case *ast.WhileStmt:
		s.Condition = w.expr(s.Condition)
		w.stmts(s.Stmts)
	case *ast.RepeatStmt:
		s.Condition = w.expr(s.Condition)
		w.stmts(s.Stmts)
	case *ast.IfStmt:
		s.Condition = w.expr(s.Condition)
		w.stmts(s.Then)
		w.stmts(s.Else)
	case *ast.NumberForStmt:
		w.reserved(s.Line(), s.Name)
		s.Init = w.expr(s.Init)
		s.Limit = w.expr(s.Limit)
		if s.Step != nil {
			s.Step = w.expr(s.Step)
		}
		w.stmts(s.Stmts)
	case *ast.GenericForStmt:
		w.names(s.Line(), s.Names)
		w.exprs(s.Exprs)
		w.stmts(s.Stmts)
	case *ast.FuncDefStmt:
		if s.Name.Func != nil {
			s.Name.Func = w.expr(s.Name.Func)
		}
		if s.Name.Receiver != nil {
			s.Name.Receiver = w.expr(s.Name.Receiver)
		}
		w.expr(s.Func)
	case *ast.ReturnStmt:
		w.exprs(s.Exprs)
	case *ast.BreakStmt, *ast.LabelStmt, *ast.GotoStmt:
	default:
		if w.err == nil {
			w.err = fmt.Errorf("line %d: unsupported statement %T", s.Line(), s)
		}
	}
}

func (w *rewriter) expr(e ast.Expr) ast.Expr {
	switch e := e.(type) {
	case *ast.StringConcatOpExpr:
		var args []ast.Expr
		w.flatten(e, &args)
		call := &ast.FuncCallExpr{Func: &ast.IdentExpr{Value: concatName}, Args: args, AdjustRet: true}
		call.SetLine(e.Line())
		call.SetLastLine(e.LastLine())
		call.Func.SetLine(e.Line())
		return call
	case *ast.IdentExpr:
		w.reserved(e.Line(), e.Value)
	case *ast.AttrGetExpr:
		e.Object = w.expr(e.Object)
		e.Key = w.expr(e.Key)
	case *ast.TableExpr:
		for _, f := range e.Fields {
			if f.Key != nil {
				f.Key = w.expr(f.Key)
			}
			f.Value = w.expr(f.Value)
		}
	case *ast.FuncCallExpr:
		if e.Func != nil {
			e.Func = w.expr(e.Func)
		}
		if e.Receiver != nil {
			e.Receiver = w.expr(e.Receiver)
		}
		w.exprs(e.Args)
	case *ast.LogicalOpExpr:
		e.Lhs, e.Rhs = w.expr(e.Lhs), w.expr(e.Rhs)
	case *ast.RelationalOpExpr:
		e.Lhs, e.Rhs = w.expr(e.Lhs), w.expr(e.Rhs)
	case *ast.ArithmeticOpExpr:
		e.Lhs, e.Rhs = w.expr(e.Lhs), w.expr(e.Rhs)
	case *ast.UnaryMinusOpExpr:
		e.Expr = w.expr(e.Expr)
	case *ast.UnaryNotOpExpr:
		e.Expr = w.expr(e.Expr)
	case *ast.UnaryLenOpExpr:
		e.Expr = w.expr(e.Expr)
	case *ast.FunctionExpr:
		if e.ParList != nil {
			w.names(e.Line(), e.ParList.Names)
		}
		w.stmts(e.Stmts)
	case *ast.TrueExpr, *ast.FalseExpr, *ast.NilExpr, *ast.NumberExpr, *ast.StringExpr, *ast.Comma3Expr:
	default:
		if w.err == nil {
			w.err = fmt.Errorf("line %d: unsupported expression %T", e.Line(), e)
		}
	}
	return e
}

// flatten collects the operands of a chain of .. left to right, rewriting
// each, so a .. b .. c becomes one call with three arguments.
func (w *rewriter) flatten(e ast.Expr, args *[]ast.Expr) {
	if c, ok := e.(*ast.StringConcatOpExpr); ok {
		w.flatten(c.Lhs, args)
		w.flatten(c.Rhs, args)
		return
	}
	e = w.expr(e)
	// An operand of .. is one value: f() .. x uses only f's first result.
	switch e := e.(type) {
	case *ast.FuncCallExpr:
		e.AdjustRet = true
	case *ast.Comma3Expr:
		e.AdjustRet = true
	}
	*args = append(*args, e)
}
