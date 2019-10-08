package graphql

import (
	"strings"

	"github.com/GannettDigital/graphql/language/ast"
)

type fieldDefiner interface {
	Fields() FieldDefinitionMap
}

// QueryComplexity returns the complexity cost of the given query.
//
// The cost is calculated by adding up the costs of the various fields
func QueryComplexity(p ExecuteParams) (int, map[string]int, error) {
	exeContext, err := buildExecutionContext(buildExecutionCtxParams{
		Schema:        p.Schema,
		Root:          p.Root,
		AST:           p.AST,
		OperationName: p.OperationName,
		Args:          p.Args,
		Errors:        nil,
		Result:        &Result{},
		Context:       p.Context,
	})
	if err != nil {
		return 0, nil, err
	}

	operationType, err := getOperationRootType(p.Schema, exeContext.Operation)
	if err != nil {
		return 0, nil, err
	}

	costDetail := make(map[string]int)
	cost := selectionSetCost(exeContext.Operation.GetSelectionSet(), operationType, exeContext, "", costDetail)

	return cost, costDetail, nil
}

// astFieldCost will recursively determine the cost of a field including its children.
func astFieldCost(field *ast.Field, fieldDef *FieldDefinition, exeContext *executionContext, basePath string, costDetail map[string]int) int {
	// Absolute path location for query complexity details.
	fieldName := strings.Builder{}
	if basePath != "" {
		fieldName.WriteString(basePath + ".")
	}
	if field.Alias != nil {
		fieldName.WriteString(field.Alias.Value + "=")
	}
	fieldName.WriteString(field.Name.Value)
	path := fieldName.String()

	cost := fieldDef.Cost
	if cost > 0 {
		costDetail[path] = cost
	}

	set := field.GetSelectionSet()
	if set == nil {
		return cost
	}
	fType := fieldDef.Type
	if nonNullType, ok := fieldDef.Type.(*NonNull); ok {
		fType = nonNullType.OfType
	}
	if listType, ok := fType.(*List); ok {
		fType = listType.OfType
	}
	if nonNullType, ok := fType.(*NonNull); ok {
		fType = nonNullType.OfType
	}
	parent, ok := fType.(fieldDefiner)
	if !ok {
		return cost
	}
	cost += selectionSetCost(set, parent, exeContext, path, costDetail)

	return cost
}

// selectionSetCost will return the cost for a given selection set.
func selectionSetCost(set *ast.SelectionSet, parent fieldDefiner, exeContext *executionContext, basePath string, costDetail map[string]int) int {
	if set == nil {
		return 0
	}
	var cost int
	var maxInlineFragmentCostDetail map[string]int
	maxInlineFragmentCost := 0
	for _, iSelection := range set.Selections {
		switch selection := iSelection.(type) {
		case *ast.Field:
			fieldDef, ok := parent.Fields()[selection.Name.Value]
			if !ok {
				continue
			}
			cost += astFieldCost(selection, fieldDef, exeContext, basePath, costDetail)
		case *ast.InlineFragment:
			selectionType := selection.TypeCondition
			parentInterface, ok := parent.(*Interface)
			if !ok || selectionType == nil || parentInterface == nil {
				cost += selectionSetCost(selection.SelectionSet, parent, exeContext, basePath, costDetail)
				continue
			}
			for _, object := range exeContext.Schema.implementations[parentInterface.Name()] {
				if object.Name() == selectionType.Name.Value {
					// calculate here
					inlineFragmentCostDetail := make(map[string]int)
					inlineFragmentCost := selectionSetCost(selection.SelectionSet, object, exeContext, basePath, inlineFragmentCostDetail)
					if inlineFragmentCost > maxInlineFragmentCost {
						maxInlineFragmentCost = inlineFragmentCost
						maxInlineFragmentCostDetail = inlineFragmentCostDetail
					}
					break
				}
			}
		case *ast.FragmentSpread:
			fragment, ok := exeContext.Fragments[selection.Name.Value]
			if !ok {
				continue
			}
			fragmentDef, ok := fragment.(*ast.FragmentDefinition)
			if !ok {
				continue
			}
			fragmentType, err := typeFromAST(exeContext.Schema, fragmentDef.TypeCondition)
			if err != nil {
				continue
			}
			fragmentObject, ok := fragmentType.(fieldDefiner)
			if !ok {
				continue
			}
			cost += selectionSetCost(fragment.GetSelectionSet(), fragmentObject, exeContext, basePath, costDetail)
		}
	}
	for mifcdKey, mifcdValue := range maxInlineFragmentCostDetail {
		costDetail[mifcdKey] = mifcdValue
	}
	return cost + maxInlineFragmentCost
}
